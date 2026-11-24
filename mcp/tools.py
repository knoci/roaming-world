"""Tool, resource, and prompt definitions + validation/projection helpers for Roaming World MCP."""
from __future__ import annotations

import datetime
import re
import urllib.parse
from typing import Any

MAX_QUERY_LEN = 500
EXCERPT_MAX_LEN = 250
ARTICLE_CONTENT_MAX_LEN = 4000
MAX_IDEMPOTENCY_KEY_LEN = 128

ID_PATTERN = re.compile(r"^[A-Za-z0-9_-]{1,64}\Z")
DATE_PATTERN = re.compile(r"^\d{4}-\d{2}-\d{2}\Z")
VALID_ARTICLE_SORTS = ("time", "likes", "favorite")

READ_ONLY_ANNOTATIONS: dict[str, bool] = {
    "readOnlyHint": True,
    "idempotentHint": True,
    "destructiveHint": False,
}

WRITE_IDEMPOTENT_ANNOTATIONS: dict[str, bool] = {
    "readOnlyHint": False,
    "idempotentHint": True,
    "destructiveHint": False,
}


class InvalidParamsError(ValueError):
    """Raised when MCP tool, resource, or prompt parameters fail validation."""


def validate_base_url(name: str, url: str) -> str:
    cleaned = (url or "").strip().rstrip("/")
    parsed = urllib.parse.urlparse(cleaned)
    if parsed.scheme not in ("http", "https") or not parsed.netloc:
        raise ValueError(
            f"invalid {name} url: scheme must be http or https with non-empty host"
        )
    return cleaned


def validate_id(name: str, value: Any) -> str:
    if not isinstance(value, str) or not ID_PATTERN.fullmatch(value):
        raise InvalidParamsError(f"invalid {name}")
    return value


def validate_query(value: Any, *, required: bool = True) -> str:
    if value is None:
        if required:
            raise InvalidParamsError("query must be a non-empty string")
        return ""
    if not isinstance(value, str):
        raise InvalidParamsError("query must be a string")
    if len(value) > MAX_QUERY_LEN:
        raise InvalidParamsError(f"query length must be <= {MAX_QUERY_LEN}")
    q = value.strip()
    if required and not q:
        raise InvalidParamsError("query must be a non-empty string")
    return q


def validate_int_param(
    args: dict[str, Any],
    key: str,
    *,
    default: int,
    minimum: int = 1,
    maximum: int = 20,
) -> int:
    if key not in args or args[key] is None:
        return default
    raw = args[key]
    if isinstance(raw, bool) or not isinstance(raw, int):
        raise InvalidParamsError(f"{key} must be an integer between {minimum} and {maximum}")
    if raw < minimum or raw > maximum:
        raise InvalidParamsError(f"{key} must be an integer between {minimum} and {maximum}")
    return raw


def validate_limit(args: dict[str, Any], default: int = 5, max_limit: int = 20) -> int:
    return validate_int_param(args, "limit", default=default, minimum=1, maximum=max_limit)


def validate_page(args: dict[str, Any], default: int = 1, max_page: int = 100) -> int:
    return validate_int_param(args, "page", default=default, minimum=1, maximum=max_page)


def validate_top_k(args: dict[str, Any], default: int = 5, max_top_k: int = 20) -> int:
    return validate_int_param(args, "top_k", default=default, minimum=1, maximum=max_top_k)


def validate_sort_by(args: dict[str, Any], default: str = "time") -> str:
    raw = args.get("sort_by", default)
    if raw is None:
        return default
    if not isinstance(raw, str) or raw not in VALID_ARTICLE_SORTS:
        raise InvalidParamsError(
            f"sort_by must be one of: {', '.join(VALID_ARTICLE_SORTS)}"
        )
    return raw


def validate_date(name: str, value: Any) -> tuple[str, datetime.date]:
    if not isinstance(value, str) or not DATE_PATTERN.fullmatch(value.strip()):
        raise InvalidParamsError(f"{name} must be a valid YYYY-MM-DD date")
    cleaned = value.strip()
    try:
        parsed = datetime.date.fromisoformat(cleaned)
    except ValueError as exc:
        raise InvalidParamsError(f"{name} must be a valid YYYY-MM-DD date") from exc
    return cleaned, parsed


def validate_budget(value: Any) -> int | float:
    if value is None:
        return 0
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise InvalidParamsError("budget must be a non-negative number")
    if value < 0:
        raise InvalidParamsError("budget must be >= 0")
    return value


def validate_non_empty_str(name: str, value: Any, *, max_len: int = 200) -> str:
    if not isinstance(value, str):
        raise InvalidParamsError(f"{name} must be a non-empty string")
    cleaned = value.strip()
    if not cleaned:
        raise InvalidParamsError(f"{name} must be a non-empty string")
    if len(cleaned) > max_len:
        raise InvalidParamsError(f"{name} length must be <= {max_len}")
    return cleaned


def truncate_text(text: Any, max_len: int = EXCERPT_MAX_LEN) -> tuple[str, bool]:
    s = str(text or "").strip()
    if len(s) <= max_len:
        return s, False
    truncated = s[: max_len - 3] + "..." if max_len > 3 else s[:max_len]
    return truncated, True


def matches_query(query: str, *parts: Any) -> bool:
    q = query.lower().strip()
    if not q:
        return True
    return q in " ".join(str(p or "") for p in parts).lower()


def make_page_envelope(
    items: list[dict[str, Any]],
    *,
    page: int,
    limit: int,
    total: int,
    has_more: bool,
    truncated: bool,
) -> dict[str, Any]:
    return {
        "items": items,
        "page": page,
        "limit": limit,
        "total": total,
        "has_more": bool(has_more),
        "truncated": bool(truncated),
    }


def project_scene(item: dict[str, Any]) -> tuple[dict[str, Any], bool]:
    desc, was_trunc = truncate_text(
        item.get("describe") or item.get("content") or "", EXCERPT_MAX_LEN
    )
    return (
        {
            "sid": item.get("sid", ""),
            "name": item.get("name", ""),
            "location": item.get("location", ""),
            "describe": desc,
        },
        was_trunc,
    )


def project_food(item: dict[str, Any]) -> tuple[dict[str, Any], bool]:
    desc, was_trunc = truncate_text(
        item.get("describe") or item.get("recipe") or item.get("content") or "",
        EXCERPT_MAX_LEN,
    )
    return (
        {
            "fid": item.get("fid", ""),
            "name": item.get("name", ""),
            "location": item.get("location", ""),
            "describe": desc,
        },
        was_trunc,
    )


def project_article(item: dict[str, Any]) -> tuple[dict[str, Any], bool]:
    excerpt, was_trunc = truncate_text(
        item.get("excerpt") or item.get("content") or item.get("describe") or "",
        EXCERPT_MAX_LEN,
    )
    return (
        {
            "aid": item.get("aid", ""),
            "title": item.get("title", ""),
            "name": item.get("name", ""),
            "likes": item.get("likes", 0),
            "publishedAt": item.get("publishedAt", item.get("published_at", "")),
            "excerpt": excerpt,
        },
        was_trunc,
    )


def project_comment(item: dict[str, Any]) -> tuple[dict[str, Any], bool]:
    content, was_trunc = truncate_text(
        item.get("content") or item.get("text") or "", EXCERPT_MAX_LEN
    )
    return (
        {
            "cid": item.get("cid", item.get("id", "")),
            "aid": item.get("aid", ""),
            "uid": item.get("uid", item.get("user_id", item.get("author", ""))),
            "content": content,
            "createdAt": item.get("createdAt", item.get("created_at", item.get("time", ""))),
        },
        was_trunc,
    )


def project_plan(item: dict[str, Any]) -> tuple[dict[str, Any], bool]:
    summary, was_trunc = truncate_text(
        item.get("summary") or item.get("description") or item.get("notes") or "",
        EXCERPT_MAX_LEN,
    )
    return (
        {
            "id": item.get("id", item.get("plan_id", "")),
            "title": item.get("title", ""),
            "city": item.get("city", ""),
            "start_date": item.get("start_date", item.get("startDate", "")),
            "end_date": item.get("end_date", item.get("endDate", "")),
            "budget": item.get("budget", 0),
            "summary": summary,
        },
        was_trunc,
    )


def project_audiobook(item: dict[str, Any]) -> tuple[dict[str, Any], bool]:
    desc, was_trunc = truncate_text(
        item.get("description") or item.get("describe") or item.get("summary") or "",
        EXCERPT_MAX_LEN,
    )
    return (
        {
            "bid": item.get("bid", item.get("id", "")),
            "name": item.get("name", item.get("title", "")),
            "author": item.get("author", ""),
            "description": desc,
        },
        was_trunc,
    )


def truncate_article_detail(data: Any) -> Any:
    """Truncate article content > 4000 chars and mark truncated=True."""
    if not isinstance(data, dict):
        return data
    out = dict(data)
    content = out.get("content")
    if isinstance(content, str) and len(content) > ARTICLE_CONTENT_MAX_LEN:
        out["content"], _ = truncate_text(content, ARTICLE_CONTENT_MAX_LEN)
        out["truncated"] = True
        return out
    nested = out.get("article")
    if isinstance(nested, dict) and isinstance(nested.get("content"), str):
        if len(nested["content"]) > ARTICLE_CONTENT_MAX_LEN:
            nested_copy = dict(nested)
            nested_copy["content"], _ = truncate_text(
                nested["content"], ARTICLE_CONTENT_MAX_LEN
            )
            nested_copy["truncated"] = True
            out["article"] = nested_copy
            out["truncated"] = True
            return out
    out.setdefault("truncated", False)
    return out


TOOLS: list[dict[str, Any]] = [
    {
        "name": "search_scenes",
        "description": "按关键词检索景点，返回分页的景点名称、地点和简介。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "minLength": 1, "maxLength": MAX_QUERY_LEN},
                "page": {"type": "integer", "minimum": 1, "maximum": 100, "default": 1},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
            },
            "required": ["query"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "get_scene",
        "description": "按 sid 获取单个景点详情。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "sid": {"type": "string", "pattern": r"^[A-Za-z0-9_-]{1,64}$"},
            },
            "required": ["sid"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "search_foods",
        "description": "按关键词检索美食，匹配名称、简介、地点和做法，返回分页摘要。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "minLength": 1, "maxLength": MAX_QUERY_LEN},
                "page": {"type": "integer", "minimum": 1, "maximum": 100, "default": 1},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
            },
            "required": ["query"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "get_random_food",
        "description": "随机推荐一道美食。",
        "inputSchema": {
            "type": "object",
            "properties": {},
            "required": [],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "search_articles",
        "description": "检索已发布游记文章，支持按时间、点赞或收藏排序，返回分页摘要。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "minLength": 1, "maxLength": MAX_QUERY_LEN},
                "page": {"type": "integer", "minimum": 1, "maximum": 100, "default": 1},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
                "sort_by": {
                    "type": "string",
                    "enum": list(VALID_ARTICLE_SORTS),
                    "default": "time",
                },
            },
            "required": ["query"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "get_article",
        "description": "按 aid 获取单篇文章详情（正文超过 4000 字符自动截断并标记 truncated）。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "aid": {"type": "string", "pattern": r"^[A-Za-z0-9_-]{1,64}$"},
            },
            "required": ["aid"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "list_article_comments",
        "description": "按文章 aid 分页获取评论列表。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "aid": {"type": "string", "pattern": r"^[A-Za-z0-9_-]{1,64}$"},
                "page": {"type": "integer", "minimum": 1, "maximum": 100, "default": 1},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 10},
            },
            "required": ["aid"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "list_public_plans",
        "description": "列出公开旅行计划，支持按标题或城市关键词过滤并分页。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "maxLength": MAX_QUERY_LEN, "default": ""},
                "page": {"type": "integer", "minimum": 1, "maximum": 100, "default": 1},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
            },
            "required": [],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "get_plan",
        "description": "按 plan_id 获取单个旅行计划详情。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "plan_id": {"type": "string", "pattern": r"^[A-Za-z0-9_-]{1,64}$"},
            },
            "required": ["plan_id"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "search_audiobooks",
        "description": "检索漫游世界有声书，按书名、作者或简介过滤。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "maxLength": MAX_QUERY_LEN, "default": ""},
                "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
            },
            "required": [],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "get_audiobook_details",
        "description": "按 bid 获取单个有声书详情。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "bid": {"type": "string", "pattern": r"^[A-Za-z0-9_-]{1,64}$"},
            },
            "required": ["bid"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "search_knowledge_base",
        "description": "通过 AI 引擎 Hybrid RAG 检索站内知识库并返回综合回答与引用。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "query": {"type": "string", "minLength": 1, "maxLength": MAX_QUERY_LEN},
                "top_k": {"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
            },
            "required": ["query"],
        },
        "annotations": dict(READ_ONLY_ANNOTATIONS),
    },
    {
        "name": "save_travel_plan_draft",
        "description": "保存旅行计划草稿（需提供用户身份与幂等键 Idempotency-Key）。",
        "inputSchema": {
            "type": "object",
            "properties": {
                "title": {"type": "string", "minLength": 1, "maxLength": 200},
                "city": {"type": "string", "minLength": 1, "maxLength": 100},
                "start_date": {"type": "string", "pattern": r"^\d{4}-\d{2}-\d{2}$"},
                "end_date": {"type": "string", "pattern": r"^\d{4}-\d{2}-\d{2}$"},
                "budget": {"type": "number", "minimum": 0, "default": 0},
                "days": {"type": "array", "items": {"type": "object"}},
                "idempotency_key": {
                    "type": "string",
                    "minLength": 1,
                    "maxLength": MAX_IDEMPOTENCY_KEY_LEN,
                },
            },
            "required": ["title", "city", "start_date", "end_date", "idempotency_key"],
        },
        "annotations": dict(WRITE_IDEMPOTENT_ANNOTATIONS),
    },
]


RESOURCE_TEMPLATES: list[dict[str, Any]] = [
    {
        "uriTemplate": "roaming://scene/{sid}",
        "name": "Scene Detail",
        "description": "读取指定 sid 的景点详情 JSON。",
        "mimeType": "application/json",
    },
    {
        "uriTemplate": "roaming://article/{aid}",
        "name": "Article Detail",
        "description": "读取指定 aid 的游记文章详情 JSON。",
        "mimeType": "application/json",
    },
    {
        "uriTemplate": "roaming://plan/{id}",
        "name": "Travel Plan Detail",
        "description": "读取指定 id 的旅行计划详情 JSON。",
        "mimeType": "application/json",
    },
]

RESOURCES: list[dict[str, Any]] = [
    {
        "uri": tpl["uriTemplate"],
        "name": tpl["name"],
        "description": tpl["description"],
        "mimeType": tpl["mimeType"],
    }
    for tpl in RESOURCE_TEMPLATES
]


PROMPTS: list[dict[str, Any]] = [
    {
        "name": "travel_itinerary_planner",
        "description": "根据目的地、天数和旅行风格生成结构化行程规划提示词。",
        "arguments": [
            {
                "name": "destination",
                "description": "目的地城市或景区名称",
                "required": True,
            },
            {
                "name": "days",
                "description": "行程天数（默认 3）",
                "required": False,
            },
            {
                "name": "style",
                "description": "旅行风格，如 balanced、relaxed、cultural、foodie（默认 balanced）",
                "required": False,
            },
        ],
    },
    {
        "name": "local_food_curator",
        "description": "根据城市和饮食偏好推荐地道美食清单的提示词。",
        "arguments": [
            {
                "name": "city",
                "description": "目标城市名称",
                "required": True,
            },
            {
                "name": "preferences",
                "description": "口味或饮食偏好（可选，如清淡、辣味、夜市小吃）",
                "required": False,
            },
        ],
    },
]


def parse_resource_uri(uri: Any) -> tuple[str, str]:
    """Parse and strictly validate roaming://<kind>/<id> URIs."""
    if not isinstance(uri, str) or not uri.strip():
        raise InvalidParamsError("uri must be a non-empty string")
    raw = uri.strip()
    if not raw.startswith("roaming://"):
        raise InvalidParamsError("unsupported resource uri scheme")
    rest = raw[len("roaming://") :]
    if "?" in rest or "#" in rest or "\\" in rest:
        raise InvalidParamsError("invalid resource uri")
    parts = rest.split("/")
    if len(parts) != 2:
        raise InvalidParamsError("invalid resource uri format")
    kind, ident = parts
    if kind not in ("scene", "article", "plan"):
        raise InvalidParamsError(f"unknown resource type: {kind}")
    id_name = {"scene": "sid", "article": "aid", "plan": "id"}[kind]
    validated_id = validate_id(id_name, ident)
    return kind, validated_id


def render_prompt(name: Any, arguments: Any = None) -> dict[str, Any]:
    """Validate and render an MCP prompt."""
    if not isinstance(name, str) or not name.strip():
        raise InvalidParamsError("prompt name is required")
    if arguments is None:
        args: dict[str, Any] = {}
    elif isinstance(arguments, dict):
        args = arguments
    else:
        raise InvalidParamsError("prompt arguments must be an object")

    if name == "travel_itinerary_planner":
        destination = validate_non_empty_str("destination", args.get("destination"), max_len=100)
        days_raw = args.get("days", "3")
        days = str(days_raw).strip() if days_raw is not None and str(days_raw).strip() else "3"
        style_raw = args.get("style", "balanced")
        style = (
            str(style_raw).strip()
            if style_raw is not None and str(style_raw).strip()
            else "balanced"
        )
        text = (
            f"请为「{destination}」规划一份 {days} 天的旅行行程，整体风格为 {style}。\n"
            f"请优先调用站内 MCP 工具（search_scenes、search_foods、search_articles、list_public_plans）"
            f"检索景点、美食与参考路线，按天输出上午/下午/晚间安排、交通建议与预算估算。"
        )
        return {
            "description": "结构化旅行行程规划提示词",
            "messages": [
                {"role": "user", "content": {"type": "text", "text": text}},
            ],
        }

    if name == "local_food_curator":
        city = validate_non_empty_str("city", args.get("city"), max_len=100)
        pref_raw = args.get("preferences", "")
        preferences = str(pref_raw).strip() if pref_raw is not None else ""
        pref_clause = f"，饮食偏好：{preferences}" if preferences else ""
        text = (
            f"请为前往「{city}」的旅行者策展一份地道美食清单{pref_clause}。\n"
            f"请调用 search_foods、get_random_food 和 search_articles 检索本地特色菜品与口碑推荐，"
            f"按早茶/正餐/夜市分类给出必吃理由与点单建议。"
        )
        return {
            "description": "城市地道美食策展提示词",
            "messages": [
                {"role": "user", "content": {"type": "text", "text": text}},
            ],
        }

    raise InvalidParamsError(f"unknown prompt: {name}")
