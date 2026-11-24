import asyncio
import json
import logging
import os
import secrets
import time
from contextlib import asynccontextmanager

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.responses import PlainTextResponse
from pydantic import BaseModel, Field

try:
    from fastapi.responses import StreamingResponse
except ImportError:  # stdlib stub test fallback
    class StreamingResponse:  # type: ignore[no-redef]
        def __init__(self, content, media_type="text/event-stream", headers=None, status_code=200):
            self.body_iterator = content
            self.media_type = media_type
            self.headers = headers or {}
            self.status_code = status_code

from .agent import run_agent
from .answer import extractive_answer
from .db import close_async_pool, connection, ensure_schema, ingest_all, init_async_pool, search
from .langgraph_flow import LangGraphAgent
from .mcp_client import MCPClient
from .metrics import record_http_request, render_metrics
from .multi_agent import AsyncMultiAgentOrchestrator, MultiAgent, MultiAgentError
from .providers import ProviderError, provider_status

logging.basicConfig(level=logging.INFO)
log = logging.getLogger("ai-engine")

_orchestrator: AsyncMultiAgentOrchestrator | None = None


def get_orchestrator() -> AsyncMultiAgentOrchestrator:
    """Return the pre-compiled AsyncMultiAgentOrchestrator singleton."""
    global _orchestrator
    if _orchestrator is None:
        _orchestrator = AsyncMultiAgentOrchestrator(MCPClient())
    return _orchestrator


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _orchestrator
    try:
        with connection() as conn:
            ensure_schema(conn)
        log.info("ai-engine schema ready")
    except Exception as exc:
        log.warning("ai-engine schema not ready: %s", exc)

    try:
        await init_async_pool()
        log.info("ai-engine async db pool ready")
    except Exception as exc:
        log.info("ai-engine async db pool not initialized: %s", exc)

    _orchestrator = AsyncMultiAgentOrchestrator(MCPClient())
    log.info("ai-engine async multi-agent orchestrator compiled")
    try:
        yield
    finally:
        try:
            await close_async_pool()
        except Exception as exc:
            log.warning("ai-engine async db pool close warning: %s", exc)


app = FastAPI(title="Roaming World AI Engine", version="0.2.0", lifespan=lifespan)


@app.middleware("http")
async def metrics_middleware(request: Request, call_next):
    start = time.perf_counter()
    route = getattr(getattr(request, "url", None), "path", "/")
    method = getattr(request, "method", "GET")
    try:
        response = await call_next(request)
        status = getattr(response, "status_code", 200)
    except Exception:
        record_http_request(route, method, 500, time.perf_counter() - start)
        raise
    record_http_request(route, method, status, time.perf_counter() - start)
    return response


@app.get("/metrics")
def metrics():
    text, content_type = render_metrics()
    try:
        return PlainTextResponse(text, media_type=content_type)
    except TypeError:
        return PlainTextResponse(text)


class ChatRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=2000)
    top_k: int = Field(5, ge=1, le=10)


class StreamRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=2000)
    thread_id: str = Field("", max_length=128)
    save_plan: bool = False


class ResumeRequest(BaseModel):
    approved: bool
    idempotency_key: str = Field("", max_length=128)


class Citation(BaseModel):
    source_type: str
    source_id: str
    title: str
    url: str = ""
    score: float = 0


class ChatResponse(BaseModel):
    answer: str
    citations: list[Citation]


def format_sse_event(event: str, data: dict) -> str:
    return f"event: {event}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n"


@app.get("/healthz")
async def healthz():
    return {"status": "ok"}


@app.get("/readyz")
def readyz():
    try:
        with connection() as conn:
            with conn.cursor() as cur:
                cur.execute("SELECT 1")
        return {"status": "ready"}
    except Exception:
        log.exception("readyz failed")
        raise HTTPException(status_code=503, detail="database not ready")


def _hdr(val: object) -> str:
    return val if isinstance(val, str) and val else "-"


def _forward_headers(
    x_request_id: str | None = None,
    x_user_id: str | None = None,
    traceparent: str | None = None,
) -> dict[str, str]:
    headers: dict[str, str] = {}
    if isinstance(x_request_id, str) and x_request_id.strip():
        headers["X-Request-ID"] = x_request_id.strip()
    if isinstance(x_user_id, str) and x_user_id.strip():
        headers["X-User-ID"] = x_user_id.strip()
    if isinstance(traceparent, str) and traceparent.strip():
        headers["traceparent"] = traceparent.strip()
    return headers


def require_token(name: str, provided: str | None):
    expected = os.getenv(name)
    if not expected:
        raise HTTPException(status_code=503, detail="internal authentication unavailable")
    if not provided or not secrets.compare_digest(provided.encode(), expected.encode()):
        raise HTTPException(status_code=401, detail="unauthorized")


def require_gateway(x_ai_gateway_token: str | None = Header(default=None)):
    require_token("AI_GATEWAY_TOKEN", x_ai_gateway_token)


def require_admin(x_ingest_token: str | None = Header(default=None)):
    require_token("AI_INGEST_TOKEN", x_ingest_token)


@app.post("/v1/ai/ingest", dependencies=[Depends(require_admin)])
def ingest():
    try:
        with connection() as conn:
            result = ingest_all(conn)
        return {"status": "ok", **result}
    except Exception:
        log.exception("ingest failed")
        raise HTTPException(status_code=503, detail="ingest failed")


class AgentResponse(BaseModel):
    answer: str
    citations: list[Citation]
    tool_calls: list[dict]
    plan: list[dict]
    review: str = ""
    ok: bool = True
    backend: str = "deterministic"
    agents: list[dict] = []


@app.get("/v1/ai/providers", dependencies=[Depends(require_admin)])
def providers():
    return provider_status()


@app.post("/v1/ai/stream", dependencies=[Depends(require_gateway)])
async def stream_chat(
    req: StreamRequest,
    x_user_id: str | None = Header(default=None, alias="X-User-ID"),
    x_user_role: str | None = Header(default=None, alias="X-User-Role"),
    x_request_id: str | None = Header(default=None, alias="X-Request-ID"),
    traceparent: str | None = Header(default=None, alias="traceparent"),
):
    query = req.query.strip()
    if not query:
        raise HTTPException(status_code=400, detail="query is required")
    log.info(
        "endpoint=/v1/ai/stream request_id=%s user_id=%s user_role=%s",
        _hdr(x_request_id), _hdr(x_user_id), _hdr(x_user_role),
    )
    orch = get_orchestrator()
    fwd_headers = _forward_headers(x_request_id, x_user_id, traceparent)

    async def event_generator():
        try:
            async for evt in orch.astream(
                query,
                thread_id=req.thread_id or None,
                user_id=x_user_id or "",
                save_plan=bool(req.save_plan),
                request_headers=fwd_headers,
            ):
                yield format_sse_event(evt["event"], evt.get("data") or {})
        except asyncio.CancelledError:
            log.info(
                "sse client disconnected request_id=%s user_id=%s",
                _hdr(x_request_id), _hdr(x_user_id),
            )
            raise
        except Exception:
            log.exception(
                "stream failed request_id=%s user_id=%s",
                _hdr(x_request_id), _hdr(x_user_id),
            )
            yield format_sse_event("error", {"detail": "stream service unavailable"})

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )


@app.post("/v1/ai/threads/{thread_id}/resume", dependencies=[Depends(require_gateway)])
async def resume_thread(
    thread_id: str,
    req: ResumeRequest,
    x_user_id: str | None = Header(default=None, alias="X-User-ID"),
    x_user_role: str | None = Header(default=None, alias="X-User-Role"),
    x_request_id: str | None = Header(default=None, alias="X-Request-ID"),
    traceparent: str | None = Header(default=None, alias="traceparent"),
):
    if not isinstance(x_user_id, str) or not x_user_id.strip():
        raise HTTPException(status_code=401, detail="X-User-ID header is required")
    log.info(
        "endpoint=/v1/ai/threads/%s/resume request_id=%s user_id=%s user_role=%s approved=%s",
        thread_id, _hdr(x_request_id), _hdr(x_user_id), _hdr(x_user_role), req.approved,
    )
    orch = get_orchestrator()
    fwd_headers = _forward_headers(x_request_id, x_user_id, traceparent)
    try:
        return await orch.resume(
            thread_id=thread_id,
            approved=req.approved,
            user_id=x_user_id.strip(),
            idempotency_key=req.idempotency_key,
            request_headers=fwd_headers,
        )
    except MultiAgentError as exc:
        msg = str(exc)
        status = 404 if "no pending approval" in msg else 403 if "does not belong" in msg else 400
        raise HTTPException(status_code=status, detail=msg)
    except Exception:
        log.exception(
            "resume failed thread_id=%s request_id=%s user_id=%s",
            thread_id, _hdr(x_request_id), _hdr(x_user_id),
        )
        raise HTTPException(status_code=503, detail="resume service unavailable")


@app.post("/v1/ai/multi", response_model=AgentResponse, dependencies=[Depends(require_gateway)])
def multi_chat(
    req: ChatRequest,
    x_user_id: str | None = Header(default=None, alias="X-User-ID"),
    x_user_role: str | None = Header(default=None, alias="X-User-Role"),
    x_request_id: str | None = Header(default=None, alias="X-Request-ID"),
):
    query = req.query.strip()
    if not query:
        raise HTTPException(status_code=400, detail="query is required")
    log.info(
        "endpoint=/v1/ai/multi request_id=%s user_id=%s user_role=%s",
        _hdr(x_request_id), _hdr(x_user_id), _hdr(x_user_role),
    )
    try:
        out = MultiAgent(MCPClient()).run(query)
        return AgentResponse(
            answer=out.result.answer,
            citations=[Citation(**c.__dict__) for c in out.result.citations],
            tool_calls=out.result.tool_calls,
            plan=out.result.plan,
            review=out.review,
            ok=out.ok,
            backend=out.backend,
            agents=out.agents,
        )
    except MultiAgentError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    except ProviderError:
        log.exception(
            "multi-agent provider failed request_id=%s user_id=%s",
            _hdr(x_request_id), _hdr(x_user_id),
        )
        raise HTTPException(status_code=502, detail="upstream provider error")
    except Exception:
        log.exception(
            "multi-agent failed request_id=%s user_id=%s",
            _hdr(x_request_id), _hdr(x_user_id),
        )
        raise HTTPException(status_code=503, detail="multi-agent service unavailable")


@app.post("/v1/ai/graph", response_model=AgentResponse, dependencies=[Depends(require_gateway)])
def graph_chat(
    req: ChatRequest,
    x_user_id: str | None = Header(default=None, alias="X-User-ID"),
    x_user_role: str | None = Header(default=None, alias="X-User-Role"),
    x_request_id: str | None = Header(default=None, alias="X-Request-ID"),
):
    query = req.query.strip()
    if not query:
        raise HTTPException(status_code=400, detail="query is required")
    log.info(
        "endpoint=/v1/ai/graph request_id=%s user_id=%s user_role=%s",
        _hdr(x_request_id), _hdr(x_user_id), _hdr(x_user_role),
    )
    try:
        state = LangGraphAgent(MCPClient()).run(query)
        result = state.get("result")
        return AgentResponse(
            answer=result.answer if result else "",
            citations=[Citation(**c.__dict__) for c in (result.citations if result else [])],
            tool_calls=result.tool_calls if result else [],
            plan=result.plan if result else [],
            review=state.get("review", ""),
            ok=bool(state.get("ok", False)),
            backend=state.get("backend", "fallback"),
        )
    except Exception:
        log.exception(
            "graph failed request_id=%s user_id=%s",
            _hdr(x_request_id), _hdr(x_user_id),
        )
        raise HTTPException(status_code=503, detail="graph service unavailable")


@app.post("/v1/ai/agent", response_model=AgentResponse, dependencies=[Depends(require_gateway)])
def agent_chat(
    req: ChatRequest,
    x_user_id: str | None = Header(default=None, alias="X-User-ID"),
    x_user_role: str | None = Header(default=None, alias="X-User-Role"),
    x_request_id: str | None = Header(default=None, alias="X-Request-ID"),
):
    query = req.query.strip()
    if not query:
        raise HTTPException(status_code=400, detail="query is required")
    log.info(
        "endpoint=/v1/ai/agent request_id=%s user_id=%s user_role=%s",
        _hdr(x_request_id), _hdr(x_user_id), _hdr(x_user_role),
    )
    try:
        result = run_agent(query, MCPClient())
        return AgentResponse(
            answer=result.answer,
            citations=[Citation(**c.__dict__) for c in result.citations],
            tool_calls=result.tool_calls,
            plan=result.plan,
        )
    except Exception:
        log.exception(
            "agent failed request_id=%s user_id=%s",
            _hdr(x_request_id), _hdr(x_user_id),
        )
        raise HTTPException(status_code=503, detail="agent service unavailable")


@app.post("/v1/ai/chat", response_model=ChatResponse, dependencies=[Depends(require_gateway)])
def chat(
    req: ChatRequest,
    x_user_id: str | None = Header(default=None, alias="X-User-ID"),
    x_user_role: str | None = Header(default=None, alias="X-User-Role"),
    x_request_id: str | None = Header(default=None, alias="X-Request-ID"),
):
    query = req.query.strip()
    if not query:
        raise HTTPException(status_code=400, detail="query is required")
    log.info(
        "endpoint=/v1/ai/chat request_id=%s user_id=%s user_role=%s",
        _hdr(x_request_id), _hdr(x_user_id), _hdr(x_user_role),
    )
    try:
        with connection() as conn:
            rows = search(conn, query, max(1, min(req.top_k, 10)))
        answer, citations = extractive_answer(query, rows)
        return ChatResponse(
            answer=answer,
            citations=[Citation(**c.__dict__) for c in citations],
        )
    except Exception:
        log.exception(
            "chat failed request_id=%s user_id=%s",
            _hdr(x_request_id), _hdr(x_user_id),
        )
        raise HTTPException(status_code=503, detail="retrieval failed")
