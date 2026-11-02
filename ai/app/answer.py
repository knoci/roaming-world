from dataclasses import dataclass


@dataclass
class Citation:
    source_type: str
    source_id: str
    title: str
    url: str
    score: float


def extractive_answer(query: str, rows: list[dict]) -> tuple[str, list[Citation]]:
    if not rows:
        return "没有检索到相关内容。可以换一个城市或关键词再试。", []
    lines = [f"根据站内内容，关于「{query}」可以这样回答："]
    seen = set()
    citations = []
    for row in rows:
        key = (row["source_type"], row["source_id"])
        if key in seen:
            continue
        seen.add(key)
        snippet = row["content"].strip().replace("\n", " ")
        if len(snippet) > 160:
            snippet = snippet[:160] + "..."
        lines.append(f"- {snippet} [{row['source_type']}:{row['source_id']}]")
        citations.append(Citation(
            source_type=row["source_type"], source_id=row["source_id"],
            title=row.get("title", ""), url=row.get("url", ""), score=float(row.get("score", 0)),
        ))
    lines.append("以上为站内检索结果，规划行程前请再核对开放时间和交通。")
    return "\n".join(lines), citations
