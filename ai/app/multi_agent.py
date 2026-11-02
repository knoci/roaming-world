"""LangGraph multi-agent pipeline.

The MCP-backed deterministic agent retrieves content and builds a draft.
Then a LangGraph StateGraph orchestrates three LLM roles:

  retrieve -> planner(qwen) -> reviewer(deepseek) -> writer(xiaomi)

Reviewer output decides whether to continue to the writer or fall back to
the deterministic draft. If langgraph is unavailable, the same nodes run
sequentially so the service still boots.
"""
from __future__ import annotations

import json
import time
from dataclasses import dataclass, field
from typing import Any, TypedDict

from .agent import AgentResult, run_agent
from .providers import PROVIDERS, ProviderError, chat

try:
    from langgraph.graph import END, StateGraph
    HAS_LANGGRAPH = True
except Exception:
    HAS_LANGGRAPH = False


class MultiAgentError(RuntimeError):
    pass


@dataclass
class MultiResult:
    result: AgentResult
    backend: str = "multi-agent"
    review: str = ""
    ok: bool = False
    agents: list[dict] = field(default_factory=list)


class MultiState(TypedDict, total=False):
    query: str
    base: Any
    context: str
    plan_text: str
    review_text: str
    final_text: str
    route: str
    planner_ok: bool
    reviewer_ok: bool
    writer_ok: bool
    agents: list[dict]


def _short_context(result: AgentResult) -> str:
    citations = "\n".join(f"[{c.source_type}:{c.source_id}] {c.title}" for c in result.citations)
    return f"草稿答案：\n{result.answer}\n\n引用：\n{citations}"


def _context(result: AgentResult) -> str:
    citations = "\n".join(f"[{c.source_type}:{c.source_id}] {c.title}" for c in result.citations)
    plan = json.dumps(result.plan, ensure_ascii=False)
    return f"草稿答案：\n{result.answer}\n\n引用：\n{citations}\n\n行程：\n{plan}"


class MultiAgent:
    def __init__(self, mcp, chat_fn=chat):
        self.mcp = mcp
        self.chat_fn = chat_fn
        self.graph = None
        if HAS_LANGGRAPH:
            try:
                self.graph = self._build_graph()
            except Exception:
                # Version mismatch: keep serving through sequential fallback.
                self.graph = None

    def _role(self, role: str, name: str, messages: list[dict], fallback: str, agents: list[dict]) -> tuple[str, bool]:
        provider = PROVIDERS[name]
        started = time.perf_counter()
        try:
            text = self.chat_fn(provider, messages)
            if not text or not text.strip():
                raise ProviderError(f"{name} returned empty content")
            agents.append({
                "role": role, "provider": name, "model": provider.model, "ok": True,
                "latency_ms": round((time.perf_counter() - started) * 1000, 1),
            })
            return text.strip(), True
        except Exception as exc:
            agents.append({
                "role": role, "provider": name, "model": provider.model, "ok": False,
                "latency_ms": round((time.perf_counter() - started) * 1000, 1),
                "error": str(exc)[:300],
            })
            return fallback, False

    def _retrieve(self, state: MultiState) -> dict:
        query = state.get("query", "")
        base = run_agent(query, self.mcp)
        return {
            "base": base,
            "context": _context(base),
            "agents": state.get("agents", []),
        }

    def _planner(self, state: MultiState) -> dict:
        base = state["base"]
        agents = list(state.get("agents", []))
        messages = [
            {"role": "system", "content": "你是行程规划 Agent。只能使用给定资料，保留 [类型:id] 引用，不要编造营业时间。输出中文行程。"},
            {"role": "user", "content": f"用户问题：{state['query']}\n\n资料：\n{_short_context(base)}"},
        ]
        text, ok = self._role("planner", "qwen", messages, base.answer, agents)
        return {"plan_text": text, "planner_ok": ok, "agents": agents}

    def _reviewer(self, state: MultiState) -> dict:
        base = state["base"]
        agents = list(state.get("agents", []))
        messages = [
            {"role": "system", "content": "你是审核 Agent。检查行程是否有引用、是否编造资料外事实。只输出 APPROVE 或 REVISE: 原因。"},
            {"role": "user", "content": f"用户问题：{state['query']}\n\n候选行程：\n{state['plan_text']}\n\n原始资料：\n{state['context']}"},
        ]
        text, ok = self._role("reviewer", "deepseek", messages, "APPROVE (review unavailable)", agents)
        route = "fallback" if text.upper().startswith("REVISE") else "writer"
        return {"review_text": text, "reviewer_ok": ok, "route": route, "agents": agents}

    def _writer(self, state: MultiState) -> dict:
        base = state["base"]
        agents = list(state.get("agents", []))
        messages = [
            {"role": "system", "content": "你是最终写作 Agent。根据审核意见和资料输出简洁中文答案，保留来源引用，不添加未知事实。"},
            {"role": "user", "content": f"用户问题：{state['query']}\n\n候选行程：\n{state['plan_text']}\n\n审核意见：\n{state['review_text']}\n\n资料：\n{state['context']}"},
        ]
        text, ok = self._role("writer", "xiaomi", messages, state["plan_text"], agents)
        return {"final_text": text, "writer_ok": ok, "agents": agents}

    def _fallback(self, state: MultiState) -> dict:
        return {
            "final_text": state["base"].answer,
            "writer_ok": False,
            "route": "fallback",
        }

    @staticmethod
    def _route(state: MultiState) -> str:
        return "fallback" if state.get("route") == "fallback" else "writer"

    def _build_graph(self):
        builder = StateGraph(MultiState)
        builder.add_node("retrieve", self._retrieve)
        builder.add_node("planner", self._planner)
        builder.add_node("reviewer", self._reviewer)
        builder.add_node("writer", self._writer)
        builder.add_node("fallback", self._fallback)
        builder.set_entry_point("retrieve")
        builder.add_edge("retrieve", "planner")
        builder.add_edge("planner", "reviewer")
        builder.add_conditional_edges(
            "reviewer", self._route, {"writer": "writer", "fallback": "fallback"}
        )
        builder.add_edge("writer", END)
        builder.add_edge("fallback", END)
        return builder.compile()

    def _run_sequential(self, query: str) -> MultiState:
        state: MultiState = {"query": query, "agents": []}
        state.update(self._retrieve(state))
        state.update(self._planner(state))
        state.update(self._reviewer(state))
        if self._route(state) == "fallback":
            state.update(self._fallback(state))
        else:
            state.update(self._writer(state))
        return state

    def run(self, query: str) -> MultiResult:
        missing = [n for n, p in PROVIDERS.items() if not p.configured]
        if missing:
            raise MultiAgentError("missing keys: " + ", ".join(missing))

        if self.graph is not None:
            try:
                state = self.graph.invoke({"query": query, "agents": []})
            except Exception:
                state = self._run_sequential(query)
        else:
            state = self._run_sequential(query)

        base = state["base"]
        final = state.get("final_text") or base.answer
        all_ok = bool(state.get("planner_ok") and state.get("reviewer_ok") and state.get("writer_ok"))
        revised = state.get("review_text", "").upper().startswith("REVISE")
        result = AgentResult(
            answer=final,
            citations=base.citations,
            tool_calls=base.tool_calls,
            plan=base.plan,
        )
        return MultiResult(
            result=result,
            backend="multi-agent" if all_ok and not revised else "multi-agent-fallback",
            review=state.get("review_text", ""),
            ok=all_ok and not revised,
            agents=state.get("agents", []),
        )
