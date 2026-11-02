import re
from dataclasses import dataclass, field

CITIES = re.compile(r"([\u4e00-\u9fa5]{2,8})(?:市)?(?:\d+\s*天|三天|三日|两天|二日|一天|一日)")
CITY_TAIL = re.compile(r"^([\u4e00-\u9fa5]{2,8}?)(?:景点|美食|文章|行程|旅游|怎么玩|攻略)")
DAYS = re.compile(r"(\d+)\s*天")
CN_DAYS = {"一天": 1, "一日": 1, "两天": 2, "二日": 2, "三天": 3, "三日": 3,
           "四天": 4, "五天": 5, "六天": 6, "七天": 7}


@dataclass
class Citation:
    source_type: str
    source_id: str
    title: str


@dataclass
class AgentResult:
    answer: str
    citations: list[Citation] = field(default_factory=list)
    tool_calls: list[dict] = field(default_factory=list)
    plan: list[dict] = field(default_factory=list)


def extract_city(query: str) -> str:
    m = CITIES.search(query)
    if m:
        return m.group(1)
    m = CITY_TAIL.search(query)
    return m.group(1) if m else ""


def extract_days(query: str) -> int:
    m = DAYS.search(query)
    if m:
        return max(1, min(int(m.group(1)), 7))
    for k, v in CN_DAYS.items():
        if k in query:
            return v
    return 0


def _cite(kind: str, row: dict) -> Citation:
    sid = str(row.get("sid") or row.get("fid") or row.get("aid") or "")
    title = str(row.get("title") or "") if kind == "article" else str(row.get("name") or row.get("title") or "")
    return Citation(source_type=kind, source_id=sid, title=title)


def _summary(query: str, rows: list[dict], kind: str) -> str:
    if not rows:
        return ""
    lines = [f"{kind}检索结果："]
    for row in rows[:3]:
        name = row.get("name") or row.get("title") or ""
        desc = row.get("describe") or row.get("content") or ""
        desc = desc.replace("\n", " ")[:100]
        lines.append(f"- {name}：{desc}")
    return "\n".join(lines)


def run_agent(query: str, mcp) -> AgentResult:
    query = (query or "").strip()
    if not query:
        return AgentResult(answer="请输入一个目的地或问题。")

    city = extract_city(query) or query[:10]
    days = extract_days(query)
    calls: list[dict] = []
    citations: list[Citation] = []
    groups: dict[str, list[dict]] = {}

    for kind, tool in (("scene", "search_scenes"), ("food", "search_foods"), ("article", "search_articles")):
        try:
            rows = mcp.call_tool(tool, {"query": city, "limit": 8})
        except Exception as exc:
            calls.append({"tool": tool, "ok": False, "error": str(exc)})
            rows = []
        if isinstance(rows, dict):
            rows = [rows]
        if not isinstance(rows, list):
            rows = []
        groups[kind] = rows
        calls.append({"tool": tool, "ok": True, "count": len(rows)})
        for row in rows[:3]:
            c = _cite(kind, row)
            if c.source_id:
                citations.append(c)

    if not any(groups.values()):
        return AgentResult(answer="没有检索到相关内容，可以换一个城市或关键词。", citations=[], tool_calls=calls)

    if days:
        plan = []
        scenes = groups["scene"] or []
        foods = groups["food"] or []
        for d in range(1, days + 1):
            morning = scenes[(d - 1) % len(scenes)] if scenes else None
            afternoon = foods[(d - 1) % len(foods)] if foods else None
            plan.append({
                "day": d,
                "morning": morning.get("name", "") if morning else "",
                "afternoon": afternoon.get("name", "") if afternoon else "",
                "note": "开放时间/交通需出发前核对",
            })
        lines = [f"{city} {days} 天行程草稿："]
        for item in plan:
            lines.append(f"Day {item['day']}：上午 {item['morning'] or '自由安排'}；下午 {item['afternoon'] or '自由安排'}")
        if groups["article"]:
            lines.append("")
            lines.append("可参考的文章：")
            for row in groups["article"][:3]:
                lines.append(f"- {row.get('title','')} [article:{row.get('aid','')}]")
        lines.append("")
        lines.append("以上是基于站内内容的草稿，出发前请核对开放时间、预约和交通。")
        return AgentResult(answer="\n".join(lines), citations=citations, tool_calls=calls, plan=plan)

    parts = [
        _summary(city, groups["scene"], "景点"),
        _summary(city, groups["food"], "美食"),
        _summary(city, groups["article"], "文章"),
    ]
    answer = "\n\n".join(p for p in parts if p)
    answer += "\n\n以上来自站内检索，具体信息请以现场为准。"
    return AgentResult(answer=answer, citations=citations, tool_calls=calls)
