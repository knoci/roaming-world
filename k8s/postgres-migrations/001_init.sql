-- Roaming World PostgreSQL schema (source of truth for fresh databases).
-- GORM AutoMigrate remains as a development fallback, but this file should be
-- applied before production rollout.

BEGIN;

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS users (
    uid         varchar(36) PRIMARY KEY,
    name        varchar(50) NOT NULL,
    avatar      varchar(255),
    password    text NOT NULL,
    email       varchar(100) NOT NULL,
    role        varchar(20) NOT NULL DEFAULT 'user',
    status      varchar(20) NOT NULL DEFAULT 'active',
    created_at  timestamptz,
    updated_at  timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_users_name ON users (name);
CREATE UNIQUE INDEX IF NOT EXISTS uk_users_email ON users (email);
CREATE INDEX IF NOT EXISTS idx_users_role ON users (role);
CREATE INDEX IF NOT EXISTS idx_users_status ON users (status);
ALTER TABLE users ADD COLUMN IF NOT EXISTS role varchar(20) NOT NULL DEFAULT 'user';
ALTER TABLE users ADD COLUMN IF NOT EXISTS status varchar(20) NOT NULL DEFAULT 'active';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS status varchar(20) NOT NULL DEFAULT 'published';
ALTER TABLE articles ADD COLUMN IF NOT EXISTS published_at timestamptz;
ALTER TABLE articles ADD COLUMN IF NOT EXISTS reviewed_by varchar(36);
ALTER TABLE articles ADD COLUMN IF NOT EXISTS review_reason text;
ALTER TABLE comments ADD COLUMN IF NOT EXISTS status varchar(20) NOT NULL DEFAULT 'active';
ALTER TABLE comments ADD COLUMN IF NOT EXISTS deleted_at timestamptz;




CREATE TABLE IF NOT EXISTS scenes (
    sid         varchar(36) PRIMARY KEY,
    name        varchar(100) NOT NULL,
    describe    varchar(400) NOT NULL,
    view        text NOT NULL,
    location    varchar(30) NOT NULL,
    article     text NOT NULL,
    created_at  timestamptz,
    updated_at  timestamptz
);

CREATE TABLE IF NOT EXISTS foods (
    fid         varchar(36) PRIMARY KEY,
    view        text NOT NULL,
    name        varchar(50) NOT NULL,
    describe    text,
    recipe      text,
    article     text,
    location    varchar(30) NOT NULL,
    created_at  timestamptz,
    updated_at  timestamptz
);

CREATE TABLE IF NOT EXISTS articles (
    aid         varchar(36) PRIMARY KEY,
    uid         varchar(36),
    name        varchar(50) NOT NULL,
    avatar      varchar(255),
    title       varchar(100) NOT NULL,
    view        text NOT NULL,
    content     text NOT NULL,
    likes       integer DEFAULT 0,
    comments    integer DEFAULT 0,
    favorite    integer DEFAULT 0,
    video       boolean DEFAULT false,
    videoid     integer DEFAULT 0,
    height      integer DEFAULT 0,
    status      varchar(20) NOT NULL DEFAULT 'published',
    published_at timestamptz,
    reviewed_by varchar(36),
    review_reason text,
    created_at  timestamptz,
    updated_at  timestamptz
);
CREATE INDEX IF NOT EXISTS idx_articles_uid ON articles (uid);
CREATE INDEX IF NOT EXISTS idx_articles_video_videoid ON articles (video, videoid);
CREATE INDEX IF NOT EXISTS idx_articles_created_at ON articles (created_at);
CREATE INDEX IF NOT EXISTS idx_articles_status ON articles (status);

CREATE TABLE IF NOT EXISTS likes (
    id          bigserial PRIMARY KEY,
    uid         varchar(36) NOT NULL,
    aid         varchar(36) NOT NULL,
    created_at  timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_like_uid_aid ON likes (uid, aid);

CREATE TABLE IF NOT EXISTS favorites (
    id          bigserial PRIMARY KEY,
    uid         varchar(36) NOT NULL,
    aid         varchar(36) NOT NULL,
    created_at  timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_favorite_uid_aid ON favorites (uid, aid);

CREATE TABLE IF NOT EXISTS comments (
    cid         varchar(36) PRIMARY KEY,
    target      varchar(36),
    content     text NOT NULL,
    likes       integer DEFAULT 0,
    uid         varchar(36),
    name        varchar(50) NOT NULL,
    avatar      varchar(255),
    replycid    varchar(36),
    replyname   varchar(50),
    status      varchar(20) NOT NULL DEFAULT 'active',
    deleted_at  timestamptz,
    created_at  timestamptz,
    updated_at  timestamptz
);
CREATE INDEX IF NOT EXISTS idx_comments_target_reply_time ON comments (target, replycid, created_at);
CREATE INDEX IF NOT EXISTS idx_comments_uid ON comments (uid);
CREATE INDEX IF NOT EXISTS idx_comments_reply_time ON comments (replycid, created_at);
CREATE INDEX IF NOT EXISTS idx_comments_status ON comments (status);

CREATE TABLE IF NOT EXISTS commentlikes (
    id          bigserial PRIMARY KEY,
    uid         varchar(36) NOT NULL,
    cid         varchar(36) NOT NULL,
    created_at  timestamptz,
    updated_at  timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_commentlike_uid_cid ON commentlikes (uid, cid);

CREATE TABLE IF NOT EXISTS audiobooks (
    bid         varchar(36) PRIMARY KEY,
    view        varchar(255),
    author      varchar(50) NOT NULL,
    name        varchar(50) NOT NULL,
    playcount   integer DEFAULT 0,
    chapternum  integer DEFAULT 0,
    rating      double precision DEFAULT 9.4,
    description text,
    created_at  timestamptz,
    updated_at  timestamptz
);

CREATE TABLE IF NOT EXISTS audiobook_details (
    did         varchar(36) PRIMARY KEY,
    bid         varchar(36),
    chapter     integer NOT NULL DEFAULT 1,
    audio       varchar(255) NOT NULL,
    name        varchar(255) NOT NULL,
    duration    integer NOT NULL,
    created_at  timestamptz,
    updated_at  timestamptz
);
CREATE INDEX IF NOT EXISTS idx_audiobook_details_bid_chapter ON audiobook_details (bid, chapter);

-- Transactional outbox for comment count events. comment writes this row in the
-- same transaction as the comment, then a background publisher sends Kafka.
CREATE TABLE IF NOT EXISTS comment_outbox (
    id            bigserial PRIMARY KEY,
    event_id      varchar(36) NOT NULL,
    topic         varchar(100) NOT NULL,
    event_key     varchar(64) NOT NULL,
    payload       bytea NOT NULL,
    status        varchar(20) NOT NULL DEFAULT 'pending',
    retry_count   integer NOT NULL DEFAULT 0,
    next_retry_at timestamptz NOT NULL,
    created_at    timestamptz,
    published_at  timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_comment_outbox_event_id ON comment_outbox (event_id);
CREATE INDEX IF NOT EXISTS idx_comment_outbox_key ON comment_outbox (event_key);
CREATE INDEX IF NOT EXISTS idx_comment_outbox_pending ON comment_outbox (status, next_retry_at);

-- Travel plan aggregate.
CREATE TABLE IF NOT EXISTS travel_plans (
    id          varchar(36) PRIMARY KEY,
    uid         varchar(36) NOT NULL,
    title       varchar(100) NOT NULL,
    city        varchar(50),
    start_date  date NOT NULL,
    end_date    date NOT NULL,
    budget      integer NOT NULL DEFAULT 0,
    visibility  varchar(20) NOT NULL DEFAULT 'private',
    status      varchar(20) NOT NULL DEFAULT 'draft',
    cover       varchar(255),
    created_at  timestamptz,
    updated_at  timestamptz
);
CREATE INDEX IF NOT EXISTS idx_travel_plans_uid ON travel_plans (uid);
CREATE INDEX IF NOT EXISTS idx_travel_plans_status ON travel_plans (status);

CREATE TABLE IF NOT EXISTS travel_plan_days (
    id       varchar(36) PRIMARY KEY,
    plan_id  varchar(36) NOT NULL,
    day_no   integer NOT NULL,
    date     date NOT NULL,
    title    varchar(100),
    CONSTRAINT uk_plan_day UNIQUE (plan_id, day_no)
);
CREATE INDEX IF NOT EXISTS idx_travel_plan_days_plan ON travel_plan_days (plan_id);

CREATE TABLE IF NOT EXISTS travel_plan_items (
    id          varchar(36) PRIMARY KEY,
    plan_id     varchar(36) NOT NULL,
    day_id      varchar(36) NOT NULL,
    item_type   varchar(20) NOT NULL,
    item_id     varchar(36) NOT NULL,
    start_time  varchar(5) NOT NULL,
    end_time    varchar(5) NOT NULL,
    note        text,
    sort_order  integer NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_travel_plan_items_plan ON travel_plan_items (plan_id);
CREATE INDEX IF NOT EXISTS idx_travel_plan_items_day ON travel_plan_items (day_id);

CREATE TABLE IF NOT EXISTS comment_reports (
    id           varchar(36) PRIMARY KEY,
    cid          varchar(36) NOT NULL,
    reporter_uid varchar(36) NOT NULL,
    reason       varchar(500) NOT NULL,
    status       varchar(20) NOT NULL DEFAULT 'pending',
    reviewed_by  varchar(36),
    review_note  text,
    created_at   timestamptz,
    updated_at   timestamptz,
    CONSTRAINT uk_comment_report_user UNIQUE (cid, reporter_uid)
);
CREATE INDEX IF NOT EXISTS idx_comment_reports_status ON comment_reports (status);
CREATE INDEX IF NOT EXISTS idx_comment_reports_cid ON comment_reports (cid);

CREATE TABLE IF NOT EXISTS follows (
    id           bigserial PRIMARY KEY,
    follower_uid varchar(36) NOT NULL,
    followee_uid varchar(36) NOT NULL,
    created_at   timestamptz,
    CONSTRAINT uk_follow_pair UNIQUE (follower_uid, followee_uid)
);
CREATE INDEX IF NOT EXISTS idx_follows_followee ON follows (followee_uid);

CREATE TABLE IF NOT EXISTS notifications (
    id         varchar(36) PRIMARY KEY,
    uid        varchar(36) NOT NULL,
    type       varchar(30) NOT NULL,
    actor_uid  varchar(36),
    actor_name varchar(50),
    payload    text,
    read_at    timestamptz,
    created_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_notifications_uid_read ON notifications (uid, read_at);

COMMIT;
