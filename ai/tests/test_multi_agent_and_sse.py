import asyncio
import json
import unittest

from ai.app.multi_agent import (
    AsyncMultiAgentOrchestrator,
    InMemoryCheckpointStore,
    MultiAgentError,
)
from ai.app.providers import Provider, ProviderCircuitBreaker
from ai.evals.run_eval import run_evaluation


class _FakeAsyncMCP:
    def __init__(self, *, track_concurrency: bool = False):
        self.calls: list[dict] = []
        self.saved_drafts: list[dict] = []
        self._active = 0
        self.max_concurrent = 0
        self._track_concurrency = track_concurrency

    async def acall_tool(self, name: str, arguments: dict, *, headers: dict | None = None):
        self._active += 1
        if self._active > self.max_concurrent:
            self.max_concurrent = self._active
        try:
            if self._track_concurrency:
                await asyncio.sleep(0.02)
            self.calls.append({"tool": name, "arguments": arguments, "headers": headers or {}})
            if name == "search_scenes":
                return [
                    {
                        "sid": "s_cd_panda",
                        "name": "成都大熊猫繁育研究基地",
                        "describe": "建议早上入园看大熊猫。",
                        "location": "成都",
                    }
                ]
            if name == "search_foods":
                return [
                    {
                        "fid": "f_cd_hotpot",
                        "name": "成都老火锅",
                        "describe": "麻辣鲜香，推荐微辣锅底。",
                        "location": "成都",
                    }
                ]
            if name == "list_public_plans":
                return {
                    "items": [
                        {
                            "id": "p_cd_2d",
                            "title": "成都两日慢游",
                            "city": "成都",
                            "description": "第一天看熊猫吃火锅，第二天游锦里。",
                        }
                    ]
                }
            if name == "search_articles":
                return [
                    {
                        "aid": "a_cd_guide",
                        "title": "成都周末游攻略",
                        "excerpt": "地铁直达熊猫基地和春熙路。",
                    }
                ]
            if name == "save_travel_plan_draft":
                record = {
                    "id": "plan-saved-001",
                    **arguments,
                    "headers": dict(headers or {}),
                }
                self.saved_drafts.append(record)
                return record
            return []
        finally:
            self._active -= 1


class _FakeRetriever:
    def retrieve(self, query: str, top_k: int = 5) -> list[dict]:
        return [
            {
                "source_type": "scene",
                "source_id": "s_cd_panda",
                "title": "成都大熊猫繁育研究基地",
                "content": "成都大熊猫繁育研究基地早上最活跃。",
                "score": 0.92,
            }
        ][:top_k]


class AsyncMultiAgentAndSSETests(unittest.TestCase):
    def test_parallel_fanout_calls_all_specialists_concurrently(self):
        mcp = _FakeAsyncMCP(track_concurrency=True)
        orch = AsyncMultiAgentOrchestrator(
            mcp,
            retriever=_FakeRetriever(),
            achat_fn=None,
            checkpointer=InMemoryCheckpointStore(),
        )
        res = asyncio.run(orch.arun("帮我规划成都两天行程", thread_id="t-fanout", user_id="u1"))

        called_tools = {c["tool"] for c in mcp.calls}
        self.assertEqual(
            called_tools,
            {"search_scenes", "search_foods", "list_public_plans", "search_articles"},
        )
        self.assertGreaterEqual(mcp.max_concurrent, 3)
        self.assertEqual(res.intent, "planning")
        self.assertEqual(res.status, "completed")
        self.assertTrue(res.ok)

    def test_bounded_self_correction_loop_revises_then_approves(self):
        mcp = _FakeAsyncMCP()
        planner_prompts: list[str] = []
        verifier_calls = {"count": 0}
        fake_provider = Provider("mock", "MOCK_KEY", "MOCK_BASE", "MOCK_MODEL", "http://localhost", "mock-1")

        async def fake_achat(provider, messages):
            sys_msg = messages[0]["content"]
            user_msg = messages[1]["content"]
            if "事实验证 Agent" in sys_msg:
                verifier_calls["count"] += 1
                if verifier_calls["count"] == 1:
                    return json.dumps({"verdict": "revise", "issues": ["missing citation"]})
                return json.dumps({"verdict": "approve", "issues": []})
            planner_prompts.append(user_msg)
            if len(planner_prompts) == 1:
                return "成都两日游推荐：上午看大熊猫，晚上吃老火锅。"
            return "成都两日游推荐：上午看大熊猫 [scene:s_cd_panda]，晚上吃老火锅 [food:f_cd_hotpot]。"

        orch = AsyncMultiAgentOrchestrator(
            mcp,
            retriever=_FakeRetriever(),
            achat_fn=fake_achat,
            max_revisions=2,
            provider_selector=lambda _role: fake_provider,
        )
        res = asyncio.run(orch.arun("成都2天旅游规划", thread_id="t-revise", user_id="u1"))

        self.assertEqual(res.revision_count, 1)
        self.assertEqual(verifier_calls["count"], 2)
        self.assertEqual(len(planner_prompts), 2)
        self.assertIn("missing citation", planner_prompts[1])
        self.assertIn("[scene:s_cd_panda]", res.answer)
        self.assertEqual(res.verdict, "approve")
        self.assertTrue(res.ok)

    def test_hitl_approval_gate_pause_and_resume_approve_or_cancel(self):
        mcp = _FakeAsyncMCP()
        cp = InMemoryCheckpointStore()
        orch = AsyncMultiAgentOrchestrator(
            mcp,
            retriever=_FakeRetriever(),
            achat_fn=None,
            checkpointer=cp,
        )

        # 1. Pause at approval_gate when save_plan=True
        paused = asyncio.run(
            orch.arun("帮我规划成都2天行程并保存", thread_id="t-hitl-1", user_id="u-100", save_plan=True)
        )
        self.assertEqual(paused.status, "approval_required")
        self.assertIsNotNone(paused.draft_plan)
        self.assertEqual(len(mcp.saved_drafts), 0)

        # Wrong user cannot resume someone else's thread
        with self.assertRaises(MultiAgentError):
            asyncio.run(
                orch.resume(
                    "t-hitl-1",
                    approved=True,
                    user_id="other-user",
                    idempotency_key="idem-bad",
                )
            )

        # 2. Approve and persist with X-User-ID & Idempotency-Key
        resumed = asyncio.run(
            orch.resume(
                "t-hitl-1",
                approved=True,
                user_id="u-100",
                idempotency_key="idem-abc-123",
                request_headers={"X-Request-ID": "req-77"},
            )
        )
        self.assertEqual(resumed["status"], "saved")
        self.assertTrue(resumed["approved"])
        self.assertEqual(resumed["idempotency_key"], "idem-abc-123")
        self.assertEqual(len(mcp.saved_drafts), 1)
        saved = mcp.saved_drafts[0]
        self.assertEqual(saved["headers"].get("X-User-ID"), "u-100")
        self.assertEqual(saved["headers"].get("Idempotency-Key"), "idem-abc-123")
        self.assertEqual(saved["headers"].get("X-Request-ID"), "req-77")

        # 3. Cancel flow does NOT invoke save_travel_plan_draft
        paused2 = asyncio.run(
            orch.arun("成都2天攻略", thread_id="t-hitl-2", user_id="u-100", save_plan=True)
        )
        self.assertEqual(paused2.status, "approval_required")
        cancelled = asyncio.run(
            orch.resume("t-hitl-2", approved=False, user_id="u-100", idempotency_key="idem-cancel")
        )
        self.assertEqual(cancelled["status"], "cancelled")
        self.assertFalse(cancelled["approved"])
        self.assertEqual(len(mcp.saved_drafts), 1)

    def test_astream_yields_ordered_events_and_handles_cancellation(self):
        mcp = _FakeAsyncMCP()
        orch = AsyncMultiAgentOrchestrator(mcp, retriever=_FakeRetriever(), achat_fn=None)

        async def _collect_all():
            events = []
            async for evt in orch.astream("成都两天游攻略", thread_id="t-sse", user_id="u1"):
                events.append(evt)
            return events

        events = asyncio.run(_collect_all())
        names = [e["event"] for e in events]
        self.assertIn("stage", names)
        self.assertIn("tool_call", names)
        self.assertIn("citation", names)
        self.assertIn("token", names)
        self.assertEqual(names[-1], "done")

        stages = [e["data"]["stage"] for e in events if e["event"] == "stage"]
        self.assertEqual(stages[0], "router")
        self.assertEqual(stages[1], "research")
        self.assertIn("planner", stages)
        self.assertIn("verifier", stages)

        # Simulate client disconnect cancelling an in-flight research call mid-stream
        class _SlowMCP(_FakeAsyncMCP):
            def __init__(self):
                super().__init__()
                self.cancelled = False

            async def acall_tool(self, name: str, arguments: dict, *, headers: dict | None = None):
                try:
                    await asyncio.sleep(5.0)
                    return []
                except asyncio.CancelledError:
                    self.cancelled = True
                    raise

        slow_mcp = _SlowMCP()
        slow_orch = AsyncMultiAgentOrchestrator(slow_mcp, retriever=None, achat_fn=None)

        async def _cancel_midway():
            async def _consume():
                async for _ in slow_orch.astream("成都两天游攻略", thread_id="t-cancel", user_id="u1"):
                    pass

            task = asyncio.create_task(_consume())
            await asyncio.sleep(0.03)
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task

        asyncio.run(_cancel_midway())
        self.assertTrue(slow_mcp.cancelled)

    def test_provider_circuit_breaker_state_transitions(self):
        now = [100.0]
        cb = ProviderCircuitBreaker(failure_threshold=2, cooldown_seconds=10.0, clock=lambda: now[0])

        self.assertEqual(cb.state, ProviderCircuitBreaker.CLOSED)
        self.assertTrue(cb.allow_request())

        cb.record_failure()
        self.assertEqual(cb.state, ProviderCircuitBreaker.CLOSED)
        cb.record_failure()
        self.assertEqual(cb.state, ProviderCircuitBreaker.OPEN)
        self.assertFalse(cb.allow_request())

        now[0] += 10.5
        self.assertEqual(cb.state, ProviderCircuitBreaker.HALF_OPEN)
        self.assertTrue(cb.allow_request())

        cb.record_success()
        self.assertEqual(cb.state, ProviderCircuitBreaker.CLOSED)
        self.assertEqual(cb.failure_count, 0)

    def test_offline_eval_harness_passes_all_thresholds(self):
        report = run_evaluation()
        self.assertTrue(report["passed"], f"eval failed: {report['failures']}")
        self.assertEqual(report["metrics"]["total_cases"], 30)
        self.assertGreaterEqual(report["metrics"]["recall_at_5"], 0.85)
        self.assertGreaterEqual(report["metrics"]["citation_precision"], 0.95)
        self.assertEqual(report["metrics"]["injection_defense_rate"], 1.0)


if __name__ == "__main__":
    unittest.main()
