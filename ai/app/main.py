import logging
import os
import secrets
import time
from contextlib import asynccontextmanager

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.responses import PlainTextResponse
from pydantic import BaseModel, Field

from .agent import run_agent
from .answer import extractive_answer
from .db import connection, ensure_schema, ingest_all, search
from .langgraph_flow import LangGraphAgent
from .multi_agent import MultiAgent, MultiAgentError
from .providers import ProviderError, provider_status
from .mcp_client import MCPClient

logging.basicConfig(level=logging.INFO)
log = logging.getLogger("ai-engine")


@asynccontextmanager
async def lifespan(app: FastAPI):
    try:
        with connection() as conn:
            ensure_schema(conn)
        log.info("ai-engine schema ready")
    except Exception as exc:
        log.warning("ai-engine schema not ready: %s", exc)
    yield


app = FastAPI(title="Roaming World AI Engine", version="0.1.0", lifespan=lifespan)

_requests_total = 0
_errors_total = 0
_duration_seconds = 0.0


@app.middleware("http")
async def metrics_middleware(request: Request, call_next):
    global _requests_total, _errors_total, _duration_seconds
    start = time.perf_counter()
    response = await call_next(request)
    _requests_total += 1
    if response.status_code >= 500:
        _errors_total += 1
    _duration_seconds += time.perf_counter() - start
    return response


@app.get("/metrics")
def metrics():
    return PlainTextResponse(
        "# HELP ai_requests_total Total AI HTTP requests.\n"
        "# TYPE ai_requests_total counter\n"
        f"ai_requests_total {_requests_total}\n"
        "# HELP ai_errors_total Total AI 5xx responses.\n"
        "# TYPE ai_errors_total counter\n"
        f"ai_errors_total {_errors_total}\n"
        "# HELP ai_request_duration_seconds_total Cumulative request duration.\n"
        "# TYPE ai_request_duration_seconds_total counter\n"
        f"ai_request_duration_seconds_total {_duration_seconds:.6f}\n"
    )


class ChatRequest(BaseModel):
    query: str = Field(..., min_length=1, max_length=2000)
    top_k: int = Field(5, ge=1, le=10)


class Citation(BaseModel):
    source_type: str
    source_id: str
    title: str
    url: str = ""
    score: float = 0


class ChatResponse(BaseModel):
    answer: str
    citations: list[Citation]


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
