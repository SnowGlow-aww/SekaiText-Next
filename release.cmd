@echo off
chcp 65001 >nul
setlocal

for /f "delims=" %%b in ('git branch --show-current') do set "BRANCH=%%b"
if not "%BRANCH%"=="main" (
  echo Release must run from the main branch.
  exit /b 1
)
for /f "delims=" %%s in ('git status --porcelain') do (
  echo Release requires a clean working tree, including untracked files.
  exit /b 1
)

set /p VERSION="Version (e.g. 5.9.0): "
call node scripts/sync-version.mjs --release-plan "%VERSION%" >nul
if errorlevel 1 exit /b 1
set "TAG=v%VERSION%"

git show-ref --verify --quiet "refs/tags/%TAG%"
if not errorlevel 1 (
  echo Tag %TAG% already exists locally.
  exit /b 1
)
git ls-remote --exit-code --tags origin "refs/tags/%TAG%" >nul 2>nul
set "TAG_STATUS=%ERRORLEVEL%"
if "%TAG_STATUS%"=="0" (
  echo Tag %TAG% already exists on origin.
  exit /b 1
)
if not "%TAG_STATUS%"=="2" (
  echo Could not check %TAG% on origin.
  exit /b 1
)

for /f "delims=" %%v in ('node -p "require('./package.json').version"') do set CURRENT_VERSION=%%v
if "%CURRENT_VERSION%"=="%VERSION%" (
  echo Version already %VERSION%, skipping bump.
) else (
  call npm version "%VERSION%" --no-git-tag-version
  if errorlevel 1 exit /b 1
)

call node scripts/sync-version.mjs
if errorlevel 1 exit /b 1
call node scripts/sync-version.mjs --check --tag "%TAG%"
if errorlevel 1 exit /b 1

for /f "delims=" %%f in ('node scripts/sync-version.mjs --release-files "%VERSION%"') do (
  git add -- "%%f"
  if errorlevel 1 exit /b 1
)

git diff --cached --quiet
if errorlevel 1 (
  git commit -m "%TAG%"
  if errorlevel 1 exit /b 1
) else (
  echo No version changes to commit; tagging the current commit.
)

git tag "%TAG%"
if errorlevel 1 exit /b 1
git push --atomic origin main "%TAG%"
if errorlevel 1 exit /b 1

echo Release %TAG% pushed; CI will build and publish it.
