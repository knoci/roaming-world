from dataclasses import dataclass, field
import re

_CITATION_RE = re.compile(r"\[([a-zA-Z0-9_-]+):([^\[\]\s]+)\]")

_ABSTENTION_MARKERS = (
    "未找到",
    "没有找到",
    "暂无相关",
    "知识库中暂无",
    "无法确认",
    "资料不足",
    "没有相关信息",
    "无法回答",
    "not found",
    "no relevant",
    "cannot confirm",
    "insufficient information",
    "don't have enough information",
    "do not have enough information",
)


@dataclass
class GroundingReport:
    valid_citations: list[tuple[str, str]] = field(default_factory=list)
    hallucinated_citations: list[tuple[str, str]] = field(default_factory=list)
    citation_precision: float = 0.0
    grounding_ok: bool = False
    is_abstention: bool = False


def extract_citations(text: str) -> list[tuple[str, str]]:
    """Extract all ``[source_type:source_id]`` references from text in order of appearance."""
    if not text:
        return []
    return [(m.group(1), m.group(2)) for m in _CITATION_RE.finditer(text)]


def _allowed_set(allowed_citations: list[dict] | list[tuple[str, str]]) -> set[tuple[str, str]]:
    allowed: set[tuple[str, str]] = set()
    for item in allowed_citations or []:
        if isinstance(item, dict):
            st = str(item.get("source_type", "")).strip()
            sid = str(item.get("source_id", "")).strip()
            if st and sid:
                allowed.add((st, sid))
        elif isinstance(item, (tuple, list)) and len(item) >= 2:
            st = str(item[0]).strip()
            sid = str(item[1]).strip()
            if st and sid:
                allowed.add((st, sid))
    return allowed


def is_abstention_text(answer: str) -> bool:
    """Return True when the answer explicitly abstains due to missing evidence."""
    low = (answer or "").strip().lower()
    if not low:
        return False
    return any(marker in low for marker in _ABSTENTION_MARKERS)


def verify_grounding(answer: str, allowed_citations: list[dict]) -> GroundingReport:
    """Verify that every citation in ``answer`` belongs to ``allowed_citations``."""
    allowed = _allowed_set(allowed_citations)
    raw_refs = extract_citations(answer)

    valid: list[tuple[str, str]] = []
    hallucinated: list[tuple[str, str]] = []
    seen: set[tuple[str, str]] = set()
    for ref in raw_refs:
        if ref in seen:
            continue
        seen.add(ref)
        if ref in allowed:
            valid.append(ref)
        else:
            hallucinated.append(ref)

    abstention = is_abstention_text(answer)
    total = len(valid) + len(hallucinated)
    if total > 0:
        precision = len(valid) / total
    else:
        precision = 1.0 if abstention else 0.0

    grounding_ok = (len(hallucinated) == 0) and (len(valid) >= 1 or abstention)
    return GroundingReport(
        valid_citations=valid,
        hallucinated_citations=hallucinated,
        citation_precision=precision,
        grounding_ok=grounding_ok,
        is_abstention=abstention,
    )


def sanitize_answer(answer: str, allowed_citations: list[dict]) -> str:
    """Strip fabricated ``[type:id]`` citations not present in ``allowed_citations``."""
    if not answer:
        return ""
    allowed = _allowed_set(allowed_citations)

    def _replace(match: re.Match) -> str:
        ref = (match.group(1), match.group(2))
        if ref in allowed:
            return match.group(0)
        return ""

    cleaned = _CITATION_RE.sub(_replace, answer)
    cleaned = re.sub(r"[ \t]{2,}", " ", cleaned)
    cleaned = re.sub(r" +([，。！？,.!?])", r"\1", cleaned)
    return cleaned.strip()
