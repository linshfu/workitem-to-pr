# release.ps1 — 發一版：打 tag 並 push，觸發 GitHub Actions 自動 build + 發布 Release。
#   .\release.ps1 0.3.0   ->  打 tag cli-v0.3.0、push、CI 自動 build 並發布
param([Parameter(Mandatory)][string]$Version)

$ErrorActionPreference = 'Stop'
$tag = if ($Version -match '^cli-v') { $Version } else { "cli-v$Version" }

$branch = (git rev-parse --abbrev-ref HEAD).Trim()
if ($branch -ne 'main') { throw "請在 main 分支發版（目前在 $branch）" }

git tag $tag
git push origin $tag
Write-Host "已推 tag $tag，GitHub Actions 會自動 build 並發布 Release。" -ForegroundColor Green
Write-Host "看進度：https://github.com/linshfu/very-lazy/actions" -ForegroundColor Gray
