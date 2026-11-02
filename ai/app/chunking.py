def chunk_text(text: str, max_chars: int = 400, overlap: int = 60) -> list[str]:
    """Split text by paragraphs, then hard-wrap long paragraphs with overlap."""
    text = (text or "").strip()
    if not text:
        return []
    paragraphs = [p.strip() for p in text.replace("\r\n", "\n").split("\n") if p.strip()]
    chunks: list[str] = []
    current = ""
    for p in paragraphs:
        if len(p) > max_chars:
            if current:
                chunks.append(current)
                current = ""
            start = 0
            while start < len(p):
                end = min(start + max_chars, len(p))
                chunks.append(p[start:end])
                if end == len(p):
                    break
                start = end - overlap
            continue
        if not current:
            current = p
        elif len(current) + 1 + len(p) <= max_chars:
            current += "\n" + p
        else:
            chunks.append(current)
            current = p
    if current:
        chunks.append(current)
    return chunks
