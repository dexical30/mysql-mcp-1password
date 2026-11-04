# mysql-mcp-1password

MySQL에 SQL을 실행하는 가벼운 MCP 서버(stdio). 접속 정보는 **1Password에만** 두고, 디스크(설정 파일, `.env`, 로그)에는 평문 자격 증명을 남기지 않는다.

- 세션을 시작해도 1Password를 부르지 않는다. **첫 쿼리 때** Touch ID로 승인한다.
- **15분 동안 요청이 없으면 잠근다**: DB 연결을 끊고 메모리의 접속 정보를 버린다. 다음 쿼리 때 다시 승인한다.
- 기본은 읽기 전용. 쓰기는 종류별 플래그로만 켠다.
- 단일 정적 바이너리, 런타임 의존성 없음.

## 왜 만들었나

최근 공급망 공격(Shai-Hulud, TanStack 2026-05, ChainDrop 2026-08 등)은 개발자 PC에서 `.env`, 셸 히스토리, 토큰 파일, AI 코딩 도구 설정을 통째로 수집한다. 이 서버는 그런 파일 수집으로 DB 접속 정보가 새지 않게 하는 것을 목표로 한다.

## 설치

필요한 것: macOS, [1Password 앱](https://1password.com/downloads), [1Password CLI](https://developer.1password.com/docs/cli/get-started/)(`brew install 1password-cli`).

### 릴리스 바이너리 (권장)

[Releases](https://github.com/dexical30/mysql-mcp-1password/releases)에서 받는다.

```bash
VERSION=0.1.0
ARCH=$(uname -m | sed 's/x86_64/amd64/')    # arm64 또는 amd64
FILE=mysql-mcp-1password_${VERSION}_darwin_${ARCH}.tar.gz
BASE=https://github.com/dexical30/mysql-mcp-1password/releases/download/v${VERSION}

curl -fLO "$BASE/$FILE" && curl -fLO "$BASE/checksums.txt"

# 1) 체크섬 확인
shasum -a 256 --ignore-missing -c checksums.txt

# 2) (선택, gh 필요) 이 저장소의 워크플로우가 빌드한 파일인지 확인
gh attestation verify "$FILE" -R dexical30/mysql-mcp-1password

# 3) 설치
mkdir -p ~/bin && tar -xzf "$FILE" -C ~/bin mysql-mcp-1password
```

체크섬은 파일이 릴리스에 올라간 것과 같은지만 확인한다. 빌드 출처 증명(2번)은 그 파일을 이 저장소의 GitHub Actions가 태그된 소스로 빌드했다는 것까지 확인한다.

MCP 설정의 `command`에는 절대 경로(`/Users/<you>/bin/mysql-mcp-1password`)를 쓴다. PATH에 있을 필요는 없다.

바이너리는 Apple 공증(notarization)을 받지 않았다. `gh`나 `curl`로 받으면 문제없지만, **브라우저로 받으면** macOS가 실행을 막는다. 그때는 위의 확인을 거친 뒤 `xattr -d com.apple.quarantine ~/bin/mysql-mcp-1password`로 격리 표시를 지운다.

### 소스에서 빌드

Go 1.26 이상이 필요하다.

```bash
go install github.com/dexical30/mysql-mcp-1password/cmd/mysql-mcp-1password@latest   # ~/go/bin/mysql-mcp-1password
# 또는
make build && cp bin/mysql-mcp-1password ~/bin/
```

1Password 앱 → 설정 → 개발자 → **"1Password CLI와 연동"** 을 켠다. 이걸 켜야 `op`가 앱의 Touch ID로 승인을 받는다.

### 업데이트

설치 때와 같은 명령을 새 `VERSION`으로 다시 실행한다. 같은 경로(`~/bin/mysql-mcp-1password`)에 덮어쓰므로 MCP 설정은 바꾸지 않아도 된다. 실행 중인 서버는 옛 바이너리로 계속 돌기 때문에, 덮어쓴 뒤 **Claude Code를 다시 시작**해야 새 버전이 적용된다.

다른 경로에 설치했다면 MCP 설정의 `command`도 그 경로로 바꾼다.

## 설정 방식 두 가지

접속 정보를 1Password에서 가져오는 방법은 둘 중 하나를 고른다. 둘 다 설정하면 시작할 때 에러가 난다.

| | A. 필드마다 참조 | B. 항목 하나 통째로 |
|---|---|---|
| 설정 | `MYSQL_HOST`, `MYSQL_PASS` 등에 `op://` 경로를 하나씩 | `MYSQL_OP_ITEM` 하나 |
| 1Password 조회 | 첫 쿼리 때 참조마다 `op read` (Touch ID는 처음 한 번) | 첫 쿼리 때 `op item get` 한 번 |
| 메모리에 들어오는 값 | 참조한 필드만 | 항목의 모든 필드 (메모 등 포함) |
| 이럴 때 | 기존 Node 버전 설정을 그대로 쓸 때, 항목에 다른 비밀도 들어 있을 때 | DB 접속 정보만 담긴 항목을 새로 만들 때 |

### A. 필드마다 참조

기존 `@dexical30/mcp-server-mysql` 설정의 `env`를 그대로 쓰고 `command`만 바꾸면 된다.

```json
"mysql-dev": {
  "type": "stdio",
  "command": "/Users/you/bin/mysql-mcp-1password",
  "args": [],
  "env": {
    "MYSQL_HOST": "op://Dev/mysql-dev/MYSQL_HOST",
    "MYSQL_PORT": "op://Dev/mysql-dev/MYSQL_PORT",
    "MYSQL_USER": "op://Dev/mysql-dev/MYSQL_USER",
    "MYSQL_PASS": "op://Dev/mysql-dev/MYSQL_PASS",
    "MYSQL_DB": "op://Dev/mysql-dev/MYSQL_DB"
  }
}
```

- 참조 형식은 `op://<Vault>/<Item>/<Field>`(섹션이 있으면 `op://<Vault>/<Item>/<Section>/<Field>`). 1Password 앱에서 필드의 **"시크릿 참조 복사"** 로 정확한 경로를 얻을 수 있다.
- `MYSQL_PASS`는 반드시 `op://` 참조여야 한다. 평문 비밀번호를 넣으면 시작할 때 거부한다.
- 나머지는 평문 값(`"MYSQL_HOST": "127.0.0.1"`)도 받는다. `MYSQL_PORT`(기본 3306)와 `MYSQL_DB`는 생략할 수 있다.

### B. 항목 하나 통째로

1Password에서 **데이터베이스** 템플릿으로 항목을 만들고 `MYSQL_OP_ITEM=op://<Vault>/<Item>`으로 가리킨다.

| 템플릿 필드 | 용도 | 필수 |
|---|---|---|
| 서버 (`server` / id `hostname`) | 호스트 | 예 |
| 포트 (`port`) | 포트, 비우면 3306 | 아니오 |
| 사용자 이름 (`username`) | 사용자 | 예 |
| 비밀번호 (`password`) | 비밀번호 | 예 |
| 데이터베이스 (`database`) | 기본 DB, 비우면 없이 접속 | 아니오 |

필드 이름이 다르면 `MYSQL_OP_FIELD_HOST` 등으로 지정한다(라벨 또는 id, 대소문자 무시).

## Claude Code에 등록

**사용자 범위로 등록한다.** `~/.claude.json`의 `mcpServers`에 위 JSON을 넣거나, 명령으로 추가한다.

```bash
# A 방식
claude mcp add --scope user mysql-dev \
  -e MYSQL_HOST='op://Dev/mysql-dev/MYSQL_HOST' \
  -e MYSQL_PORT='op://Dev/mysql-dev/MYSQL_PORT' \
  -e MYSQL_USER='op://Dev/mysql-dev/MYSQL_USER' \
  -e MYSQL_PASS='op://Dev/mysql-dev/MYSQL_PASS' \
  -e MYSQL_DB='op://Dev/mysql-dev/MYSQL_DB' \
  -- ~/bin/mysql-mcp-1password

# B 방식
claude mcp add --scope user mysql-prod \
  -e MYSQL_OP_ITEM='op://Dev/mysql-prod' \
  -- ~/bin/mysql-mcp-1password
```

저장소 안의 `.mcp.json`(프로젝트 범위)에는 등록하지 않는 것을 권장한다. 그러면 저장소에 들어 있는 파일이 접속할 1Password 항목이나 `op` 경로를 바꿀 수 있다.

DB가 여러 개면 이름을 달리해 여러 번 등록한다.

## 환경변수

설정은 환경변수로만 받는다. `.env`를 포함해 어떤 파일도 읽지 않는다. 평문 비밀번호를 받는 변수는 없다.

| 변수 | 기본값 | 설명 |
|---|---|---|
| `MYSQL_HOST` / `MYSQL_USER` / `MYSQL_PASS` | (A 방식에서 필수) | `op://<Vault>/<Item>/<Field>`. `MYSQL_PASS`는 참조만 허용 |
| `MYSQL_PORT` / `MYSQL_DB` | 3306 / 없음 | A 방식, 생략 가능 |
| `MYSQL_OP_ITEM` | (B 방식에서 필수) | `op://<Vault>/<Item>` |
| `MYSQL_OP_PATH` | `/opt/homebrew/bin/op` → `/usr/local/bin/op` | `op` 절대 경로. PATH는 검색하지 않는다 |
| `MYSQL_IDLE_TIMEOUT` | `15m` | 이 시간 동안 요청이 없으면 잠금 |
| `MYSQL_QUERY_TIMEOUT` | `30s` | 쿼리 실행 제한 시간(승인 대기 시간 제외) |
| `MYSQL_MAX_ROWS` | `100` | 결과 행 상한. 넘으면 잘라내고 `truncated: true` |
| `MYSQL_ALLOW_INSERT` | 꺼짐 | `true`일 때만 INSERT/REPLACE 허용 |
| `MYSQL_ALLOW_UPDATE` | 꺼짐 | `true`일 때만 UPDATE 허용 |
| `MYSQL_ALLOW_DELETE` | 꺼짐 | `true`일 때만 DELETE 허용 |
| `MYSQL_ALLOW_DDL` | 꺼짐 | `true`일 때만 테이블·인덱스·뷰 DDL 허용 |
| `MYSQL_TLS` | 꺼짐 | `true`면 TLS로 접속(시스템 신뢰 저장소로 인증서 검증) |
| `MYSQL_OP_FIELD_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_DATABASE` | 템플릿 필드 | B 방식에서 1Password 필드 이름 지정 |

플래그는 값이 정확히 `true`일 때만 켜진다(`TRUE`, `1`, `yes`는 꺼짐).

## 동작

```
세션 시작        → 1Password 호출 없음
첫 쿼리          → op로 접속 정보 조회 (Touch ID) → DB 연결 → 실행
이후 쿼리        → 바로 실행, 유휴 타이머 초기화
15분 무요청      → 연결 종료, 접속 정보 폐기 (잠금)
잠금 후 쿼리     → 다시 Touch ID
```

- 잠긴 상태에서 동시에 요청이 와도 승인 창은 한 번만 뜬다.
- 승인을 거절하거나 60초 안에 응답하지 않으면 그 요청은 실패하고, 다음 요청에서 다시 승인을 요청한다.
- 거부되는 SQL은 1Password를 건드리지 않는다. 승인 창이 뜨지 않는다.
- 1Password를 **조회할 때마다** 먼저 `op` 바이너리가 1Password(Team ID `2BUA8C4S2C`)의 서명을 가졌는지 `codesign`으로 확인한다. 서버 시작 후 바이너리가 바뀌거나 `brew upgrade`로 경로가 바뀌어도 대응한다.

### 결과 행 상한

읽기 결과는 기본 **100행**까지만 돌려준다. 넘으면 그 자리에서 잘라내고 DB 연결을 끊어 남은 행을 읽지 않으며, 응답에 `"truncated": true`를 붙인다. 모델은 이걸 보고 보통 조건(`WHERE`, `LIKE`), 집계(`COUNT`, `GROUP BY`), `LIMIT/OFFSET`으로 쿼리를 좁혀 다시 조회한다.

100행이면 대부분의 작업(샘플 확인, 집계, 조건 조회)에 충분하다. 부족해지기 쉬운 경우:

- 테이블이 많은 DB에서 `SHOW TABLES`나 `information_schema`로 전체 목록을 볼 때
- 코드 테이블 전체처럼 목록 전부가 필요한 작업

이런 서버만 `env`에 `"MYSQL_MAX_ROWS": "1000"`처럼 올린다. 데이터 추출(export)은 MCP가 아니라 덤프 도구로 한다.

제한은 행 수 기준이다. 긴 텍스트나 JSON 컬럼이 있으면 100행이어도 응답이 클 수 있다.

### SQL 규칙

SQL은 Vitess 파서로 분석하며, 분류할 수 없는 것은 모두 거부한다.

| 분류 | 문장 | 실행 방식 |
|---|---|---|
| 읽기 | SELECT, UNION, WITH ... SELECT, SHOW, DESCRIBE, EXPLAIN(내부가 읽기일 때) | READ ONLY 트랜잭션, 항상 롤백 |
| 쓰기 | INSERT/REPLACE, UPDATE, DELETE | 플래그가 켜졌을 때만, 커밋 |
| 쓰기 | `INSERT ... ON DUPLICATE KEY UPDATE` | INSERT와 UPDATE 플래그가 **둘 다** 켜졌을 때만 |
| DDL | 테이블·인덱스·뷰의 CREATE/ALTER/DROP/TRUNCATE/RENAME | `MYSQL_ALLOW_DDL=true`일 때만 |
| 항상 거부 | 다중 문장, SET, USE, CALL, LOCK, LOAD DATA, 트랜잭션 제어, `SELECT ... INTO`, `FOR UPDATE`/`FOR SHARE`, DB 생성·삭제, 사용자·권한 관리, 프로시저·함수·트리거·이벤트 | - |
| 항상 거부 (함수) | `GET_LOCK` 등 advisory lock 함수, `SLEEP`, `BENCHMARK`, `LOAD_FILE`, `SOURCE_POS_WAIT`/`MASTER_POS_WAIT`, GTID 대기 함수 | - |

advisory lock은 ROLLBACK 후에도 커넥션에 남아 다른 애플리케이션을 막을 수 있어서 거부한다.

버전 주석(`/*!80401 ... */`)은 그 버전 이상의 MySQL이 실제로 실행한다. 그래서 서버 버전과 관계없이 **항상 코드로 보고** 분류한다.

## 보안 모델

### 막는 것
- 디스크 파일 수집으로 접속 정보가 새는 것. 설정에는 `op://` 참조만 있다.
- 에러 메시지를 통한 노출. Claude Code는 도구 응답을 `~/.claude/projects/`에 저장하므로, 에러에서 호스트·IP·포트·사용자명을 지운다.
- PATH에 가짜 `op`를 끼워 넣는 것(서명 확인).
- 분류기를 우회한 쓰기. 읽기는 DB 레벨의 READ ONLY 트랜잭션으로도 막힌다.

### 막지 못하는 것
- **잠금 해제 상태의 메모리.** root 권한 공격자는 실행 중인 프로세스의 메모리를 읽을 수 있다. Go는 문자열 사본을 남길 수 있어 완전한 삭제를 보장하지 못한다.
- **1Password CLI 승인 직후.** 승인은 약 10분간 유지되며, 그 사이에는 같은 터미널의 다른 프로세스도 `op`를 쓸 수 있다. **예상하지 못한 Touch ID 요청은 거절하라.**
- **쿼리 결과.** 조회한 데이터는 모델이 읽어야 하므로 Claude Code 세션 기록에 남는다. `settings.json`의 `cleanupPeriodDays`(기본 30일)로 보관 기간을 줄일 수 있다.
- **TLS 없이 접속할 때의 중간자 공격.** MySQL 8의 `caching_sha2_password`는 TLS가 없으면 첫 로그인 때 서버의 RSA 공개키를 받아 비밀번호를 암호화한다. 드라이버가 이 키를 자동으로 요청하기 때문에 네트워크 경로상의 공격자가 키를 바꿔치기할 수 있다. 가능하면 `MYSQL_TLS=true`를 쓴다.

### 최종 방어선: 전용 DB 계정
이 서버 전용 MySQL 계정을 만들고 **필요한 최소 권한만** 준다. 서버의 권한 검사는 실수를 막는 장치이고, 진짜 경계는 DB 계정 권한이다.

```sql
CREATE USER 'mcp_ro'@'%' IDENTIFIED BY '...';
GRANT SELECT ON shop.* TO 'mcp_ro'@'%';
-- 민감한 컬럼이 있으면 그 컬럼을 뺀 뷰에만 권한을 준다
```

## 실측 (2026-11-02, Apple Silicon)

| 항목 | 이 서버 | Node 버전(`@dexical30/mcp-server-mysql` 2.1.1) |
|---|---|---|
| 바이너리 | 17.4MB (`-s -w`, trimpath) | Node 런타임 + 3.8MB 번들 |
| 잠금 상태 RSS | 약 18MB* | 약 66MB(시작 직후) |
| 쿼리 50회 후 RSS | 약 26MB* | 약 79MB |

\* 측정용 MCP 클라이언트가 같은 프로세스에 포함된 값이라 실제 서버 단독보다 약간 크다.

크기의 대부분은 Vitess SQL 파서다(분류기만 넣었을 때 11.6MB). 분류 정확성을 크기보다 우선했다.

### 아직 확인하지 않은 것
Claude Code가 띄운 이 서버에서 `op`를 호출할 때, **같은 터미널 탭의 1Password CLI 승인이 공유되는지** 아직 실측하지 않았다. 1Password 문서상 승인은 터미널 세션(TTY) 단위이므로, 같은 탭에서 새로 연 세션은 10분 안이면 다시 묻지 않을 가능성이 높다. 확인 방법:

1. `MYSQL_IDLE_TIMEOUT=1m`으로 등록한다.
2. 세션을 시작한다. Touch ID가 뜨지 않아야 한다.
3. 첫 쿼리에서 한 번 뜬다.
4. 같은 세션에서 1분 안에 다시 쿼리하면 뜨지 않는다.
5. 1분 동안 쉬었다가 쿼리하면 다시 뜬다.
6. 같은 탭에서 새 세션을 열고 쿼리하면 뜨는지 기록한다.

또 **실제 1Password 항목으로 접속하는 것**은 두 방식 모두 테스트에서 가짜 `op`로만 확인했다. 첫 등록 후 쿼리 한 번으로 확인한다.

## 릴리스

`v`로 시작하는 태그를 푸시하면 `.github/workflows/release.yml`이 테스트를 돌리고 GoReleaser로 macOS arm64/amd64 바이너리를 빌드해 GitHub Release에 올리고, 빌드 출처 증명(provenance)을 붙인다. 증명은 공개 저장소에서만 저장되므로 저장소가 비공개일 때는 이 단계를 건너뛴다.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

서버가 보고하는 버전(`serverInfo.version`)은 태그에서 자동으로 들어간다.

## 개발

```bash
make test                       # 단위 테스트
scripts/itest-mysql.sh          # 테스트용 MySQL 8.4 컨테이너 (127.0.0.1:13306)
make itest                      # 통합 테스트 포함
scripts/itest-mysql.sh stop
```

## 라이선스

[MIT](LICENSE)
