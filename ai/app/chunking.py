import hashlib
import json
import re

_SENTENCE_SPLIT_RE = re.compile(r"(?<=[。！？])|(?<=[.!?])\s+")


def _split_sentences(paragraph: str) -> list[str]:
    """Split a paragraph into sentence-like segments keeping punctuation attached."""
    segments: list[str] = []
    for line in paragraph.split("\n"):
        line = line.strip()
        if not line:
            continue
        parts = [p.strip() for p in _SENTENCE_SPLIT_RE.split(line) if p and p.strip()]
        segments.extend(parts)
    return segments


def _hard_wrap(text: str, max_chars: int, overlap: int) -> list[str]:
    chunks: list[str] = []
    step = max(1, max_chars - overlap)
    start = 0
    while start < len(text):
        end = min(start + max_chars, len(text))
        chunks.append(text[start:end])
        if end == len(text):
            break
        start += step
    return chunks


def _tail_overlap(text: str, overlap: int) -> str:
    if overlap <= 0 or not text:
        return ""
    if len(text) <= overlap:
        return text
    tail = text[-overlap:]
    # Prefer starting at a clean boundary inside the tail if available
    for sep in ("。", "！", "？", ". ", "! ", "? ", "\n", " "):
        idx = tail.find(sep)
        if 0 < idx < len(tail) - 1:
            candidate = tail[idx + len(sep):].strip()
            if candidate:
                return candidate
    return tail.strip()


def chunk_text(text: str, max_chars: int = 500, overlap: int = 80) -> list[str]:
    """Split text on paragraph and sentence boundaries with cross-paragraph sliding-window overlap."""
    text = (text or "").strip()
    if not text:
        return []
    max_chars = max(1, int(max_chars))
    overlap = max(0, min(int(overlap), max_chars - 1)) if max_chars > 1 else 0

    normalized = text.replace("\r\n", "\n").replace("\r", "\n")
    paragraphs = [p.strip() for p in re.split(r"\n{2,}", normalized) if p.strip()]

    units: list[tuple[str, str]] = []  # (separator_before, segment)
    for p_idx, para in enumerate(paragraphs):
        sentences = _split_sentences(para)
        for s_idx, sent in enumerate(sentences):
            sep = "\n\n" if (s_idx == 0 and p_idx > 0) else " "
            # Avoid extra space between CJK sentences
            if sep == " " and sent and "\u4e00" <= sent[0] <= "\u9fff":
                sep = ""
            units.append((sep, sent))

    if not units:
        return []

    chunks: list[str] = []
    current = ""

    for sep, seg in units:
        if len(seg) > max_chars:
            if current:
                chunks.append(current)
                prefix = _tail_overlap(current, overlap)
                current = ""
            else:
                prefix = ""
            combined = f"{prefix}{sep}{seg}".strip() if prefix else seg
            wrapped = _hard_wrap(combined, max_chars, overlap)
            if len(wrapped) > 1:
                chunks.extend(wrapped[:-1])
                current = wrapped[-1]
            else:
                current = wrapped[0]
            continue

        if not current:
            current = seg
            continue

        joiner = sep if sep else ""
        if len(current) + len(joiner) + len(seg) <= max_chars:
            current = f"{current}{joiner}{seg}"
        else:
            chunks.append(current)
            carry = _tail_overlap(current, overlap)
            if carry and len(carry) + len(joiner) + len(seg) <= max_chars:
                current = f"{carry}{joiner}{seg}"
            elif carry and len(carry) + 1 + len(seg) <= max_chars:
                current = f"{carry}\n{seg}"
            else:
                current = seg

    if current:
        chunks.append(current)
    return chunks


def contextualize_chunks(
    chunks: list[str],
    *,
    source_type: str,
    title: str,
    location: str = "",
) -> list[str]:
    """Prepend structured metadata header to every chunk so retrieval captures location/title semantics."""
    st = (source_type or "").strip()
    loc = (location or "").strip()
    ttl = (title or "").strip()
    tag = f"[{st}|{loc}]" if loc else f"[{st}]"
    header = f"{tag} {ttl}: " if ttl else f"{tag} "

    out: list[str] = []
    for chunk in chunks:
        c = (chunk or "").strip()
        if not c:
            continue
        if c.startswith(header):
            out.append(c)
        else:
            out.append(f"{header}{c}")
    return out


def content_hash(source_type: str, title: str, content: str, location: str = "") -> str:
    """Compute a deterministic SHA-256 hex digest for document change detection."""
    payload = json.dumps(
        [
            (source_type or "").strip(),
            (title or "").strip(),
            (location or "").strip(),
            (content or "").strip(),
        ],
        ensure_ascii=False,
        separators=(",", ":"),
    ).encode("utf-8")
    return hashlib.sha256(payload).hexdigest()
