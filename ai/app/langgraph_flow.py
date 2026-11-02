"""LangGraph adapter with a deterministic fallback.

When langgraph is installed, the graph is a real StateGraph:
supervisor -> worker -> reviewer. When it is not installed, the existing
deterministic AgentGraph is used so the service still works offline.
"""
from __future__ import annotations

from typing import Any, TypedDict

from .agent import AgentResult, run_agent
from .graph import AgentGraph
from .llm import build_context, generate_answer

try:
    from langgraph.graph import END, StateGraph
    HAS_LANGGRAPH = True
except Exception:
    HAS_LANGGRAPH = False


class _GraphState(TypedDict, total=False):
    query: str
    route: str
    result: Any
    review: str
    ok: bool
    backend: str


class LangGraphAgent:
    def __init__(self, mcp):
        self.mcp = mcp
        self.graph = self._build() if HAS_LANGGRAPH else None

    def _build(self):
        mcp = self.mcp

        def supervisor(state: _GraphState):
            query = (state.get("query") or "").strip()
            return {"route": "itinerary" if query else "reject"}

        def worker(state: _GraphState):
            if state.get("route") == "reject":
                return {"result": AgentResult(answer="请输入目的地或问题。")}
            try:
                result = run_agent(state.get("query", ""), mcp)
            except Exception as exc:
                result = AgentResult(answer="工具调用失败，请稍后重试。", tool_calls=[{"error": str(exc)}])
            enhanced = generate_answer(state.get("query", ""), build_context(result.answer, result.citations))
            if enhanced:
                result.answer = enhanced
            return {"result": result, "backend": "langgraph"}

        def reviewer(state: _GraphState):
            result = state.get("result")
            if result is None:
                return {"review": "missing_result", "ok": False}
            if not result.answer.strip():
                result.answer = "没有得到有效答案，请换个问法。"
                return {"review": "empty_answer", "ok": False}
            if result.citations:
                return {"review": "cited", "ok": True}
            if "没有检索到" in result.answer:
                return {"review": "no_results", "ok": True}
            result.answer += "\n\n（注意：本次结果缺少可引用来源，请谨慎采用。）"
            return {"review": "missing_citations", "ok": False}

        builder = StateGraph(_GraphState)
        builder.add_node("supervisor", supervisor)
        builder.add_node("worker", worker)
        builder.add_node("reviewer", reviewer)
        builder.set_entry_point("supervisor")
        builder.add_edge("supervisor", "worker")
        builder.add_edge("worker", "reviewer")
        builder.add_edge("reviewer", END)
        return builder.compile()

    def run(self, query: str) -> dict:
        query = (query or "").strip()
        if self.graph is not None:
            out = self.graph.invoke({"query": query})
            out["backend"] = "langgraph"
            return out
        state = AgentGraph(self.mcp).run(query)
        return {
            "query": query,
            "route": state.route,
            "result": state.result,
            "review": state.review,
            "ok": state.ok,
            "backend": "fallback",
        }
