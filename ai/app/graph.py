"""Deterministic state graph: supervisor -> worker -> reviewer.

This mirrors the LangGraph state-machine shape without adding the dependency
yet. The worker reuses the MCP-backed agent; the reviewer enforces citations.
"""
from dataclasses import dataclass, field

from .agent import AgentResult, Citation, run_agent


@dataclass
class State:
    query: str
    route: str = ""
    result: AgentResult | None = None
    review: str = ""
    ok: bool = False
    tool_calls: list[dict] = field(default_factory=list)


class AgentGraph:
    def __init__(self, mcp):
        self.mcp = mcp

    def supervisor(self, state: State) -> State:
        state.route = "itinerary" if state.query else "reject"
        return state

    def worker(self, state: State) -> State:
        if state.route == "reject":
            state.result = AgentResult(answer="请输入目的地或问题。")
            return state
        state.result = run_agent(state.query, self.mcp)
        state.tool_calls = state.result.tool_calls
        return state

    def reviewer(self, state: State) -> State:
        if state.result is None:
            state.review = "missing_result"
            state.ok = False
            return state
        if not state.result.answer.strip():
            state.review = "empty_answer"
            state.ok = False
            state.result.answer = "没有得到有效答案，请换个问法。"
            return state
        if state.result.citations:
            state.review = "cited"
            state.ok = True
        elif "没有检索到" in state.result.answer:
            state.review = "no_results"
            state.ok = True
        else:
            state.review = "missing_citations"
            state.ok = False
            state.result.answer += "\n\n（注意：本次结果缺少可引用来源，请谨慎采用。）"
        return state

    def run(self, query: str) -> State:
        state = State(query=(query or "").strip())
        state = self.supervisor(state)
        state = self.worker(state)
        state = self.reviewer(state)
        return state
