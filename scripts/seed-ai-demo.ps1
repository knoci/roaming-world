param([switch]$Kubernetes)

$ErrorActionPreference = "Continue"

$sql = @'
INSERT INTO users (uid, name, email, password, avatar, role, status, created_at, updated_at)
VALUES ('ai-demo-user', 'aidemo', 'ai-demo@example.com', '$2b$12$k6.QcHYYFbZpU9lfXu.d6ON/wP/LojH22asDsyhnlpy79Styq7tbm', '', 'user', 'active', NOW(), NOW())
ON CONFLICT (email) DO UPDATE SET role='user', status='active';

INSERT INTO scenes (sid, name, describe, view, location, article, created_at, updated_at)
VALUES
 ('ai-scene-1', '宽窄巷子', '成都老街区，适合散步和拍照。', '[]', '成都', '宽窄巷子是成都保存较完整的清代古街。', NOW(), NOW()),
 ('ai-scene-2', '武侯祠', '成都三国文化景点。', '[]', '成都', '武侯祠是纪念诸葛亮的祠堂。', NOW(), NOW())
ON CONFLICT (sid) DO UPDATE SET name=EXCLUDED.name, describe=EXCLUDED.describe, location=EXCLUDED.location, article=EXCLUDED.article, updated_at=NOW();

INSERT INTO foods (fid, view, name, describe, recipe, article, location, created_at, updated_at)
VALUES
 ('ai-food-1', '[]', '火锅', '成都代表性美食，麻辣鲜香。', '', '成都火锅常见牛油锅底。', '成都', NOW(), NOW()),
 ('ai-food-2', '[]', '担担面', '成都传统面食。', '', '担担面以花椒和肉臊为特色。', '成都', NOW(), NOW())
ON CONFLICT (fid) DO UPDATE SET name=EXCLUDED.name, describe=EXCLUDED.describe, location=EXCLUDED.location, article=EXCLUDED.article, updated_at=NOW();

INSERT INTO articles (aid, uid, name, avatar, title, view, content, likes, comments, favorite, video, videoid, height, status, created_at, updated_at)
VALUES ('ai-article-1', 'ai-demo-user', 'aidemo', '', '成都三日游参考', '[]', '第一天宽窄巷子，第二天武侯祠，穿插成都火锅。', 0, 0, 0, false, 0, 0, 'published', NOW(), NOW())
ON CONFLICT (aid) DO UPDATE SET title=EXCLUDED.title, content=EXCLUDED.content, status='published', updated_at=NOW();
'@

if ($Kubernetes) {
  kubectl exec -n roaming-world deployment/postgres -- psql -U roaming -d roaming_world -v ON_ERROR_STOP=1 -c $sql
} else {
  docker compose exec -T postgres psql -U roaming -d roaming_world -v ON_ERROR_STOP=1 -c $sql
}
if ($LASTEXITCODE -ne 0) { Write-Host "Seed failed"; exit 1 }
Write-Host "Seeded demo user/content. Login: ai-demo@example.com / demo123456"
