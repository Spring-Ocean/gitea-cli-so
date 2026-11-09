#!/bin/sh
# 从固定 GitHub Release 安装首次使用的 macOS 程序。
# 只写用户目录；校验失败不安装，已有程序交给 sotea update 管理。
set -eu

repository='https://github.com/Spring-Ocean/gitea-cli-so'
version=''
github_auth=${SOTEA_GITHUB_AUTH:-auto}
install_directory="$HOME/.local/bin"
modify_path=true
download_directory=''
staged_binary=''

# fail 输出可操作的英文错误并停止安装。
# 调用前不删除已有安装，退出时由 trap 回收临时文件。
fail() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

# cleanup 仅删除本次创建的下载目录和暂存程序。
# 已提交的安装和用户原有文件不在清理范围内。
cleanup() {
    if [ -n "$staged_binary" ]; then
        rm -f "$staged_binary"
    fi
    if [ -n "$download_directory" ]; then
        rm -rf "$download_directory"
    fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

while [ "$#" -gt 0 ]; do
    case "$1" in
        --github-auth)
            [ "$#" -ge 2 ] || fail '--github-auth requires auto or anonymous.'
            github_auth=$2
            shift 2
            ;;
        --version)
            [ "$#" -ge 2 ] || fail '--version requires a release tag.'
            version=$2
            shift 2
            ;;
        --install-dir)
            [ "$#" -ge 2 ] || fail '--install-dir requires an absolute directory.'
            install_directory=$2
            shift 2
            ;;
        --no-modify-path)
            modify_path=false
            shift
            ;;
        --help|-h)
            printf '%s\n' 'Usage: sh install.sh [--version vX.Y.Z[-rc.N]] [--install-dir /absolute/path] [--no-modify-path] [--github-auth auto|anonymous]' \
                'Without --version, install the latest stable release. Existing installations are never overwritten.'
            exit 0
            ;;
        *) fail "Unknown option: $1" ;;
    esac
done

[ "$(uname -s)" = Darwin ] || fail 'This installer supports macOS only.'
case "$(uname -m)" in
    arm64) architecture=arm64 ;;
    x86_64) architecture=amd64 ;;
    *) fail 'This Mac architecture is not supported.' ;;
esac
case "$install_directory" in
    /*) ;;
    *) fail 'The installation directory must be absolute.' ;;
esac
case "$install_directory" in
    *'
'*) fail 'The installation directory cannot contain a newline.' ;;
esac
for dependency in curl tar shasum awk mktemp link; do
    command -v "$dependency" >/dev/null 2>&1 || fail "Required command not found: $dependency"
done
target="$install_directory/sotea"
if [ -e "$target" ] || [ -L "$target" ]; then
    fail "An installation already exists at $target. Use sotea update install."
fi

case "$github_auth" in auto|anonymous) ;; *) fail 'GitHub authentication must be auto or anonymous.' ;; esac

# auth_command 在仓库外执行只读凭证查询，并限制等待时间。
# 输出仅保留在命令替换内存中，stderr及任何登录提示均禁止。
auth_command() (
    cd "$download_directory" || exit 1
    for auth_key in $(env | sed -n 's/^\(GIT_[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$auth_key"; done
    export GIT_TERMINAL_PROMPT=0 GIT_ASKPASS='' GCM_INTERACTIVE=Never GH_PROMPT_DISABLED=1
    exec 3<&0
    "$@" <&3 2>/dev/null &
    auth_pid=$!
    (sleep 5; kill "$auth_pid" 2>/dev/null) >/dev/null 2>&1 &
    auth_timer=$!
    auth_status=0
    wait "$auth_pid" || auth_status=$?
    kill "$auth_timer" 2>/dev/null || :
    wait "$auth_timer" 2>/dev/null || :
    exit "$auth_status"
)

# github_token 按明确优先级读取固定github.com的凭证，不写入任何存储。
# Git helper只能提供完整匹配的HTTPS上下文；缺失或失败回退匿名。
github_token() {
    for auth_value in "${SOTEA_GITHUB_TOKEN:-}" "${GH_TOKEN:-}" "${GITHUB_TOKEN:-}"; do
        case "$auth_value" in ''|*[!A-Za-z0-9_.-]*) ;; *) printf '%s' "$auth_value"; return ;; esac
    done
    auth_value=$(auth_command gh auth token --hostname github.com) || auth_value=''
    case "$auth_value" in ''|*[!A-Za-z0-9_.-]*) ;; *) printf '%s' "$auth_value"; return ;; esac
    auth_reply=$(printf 'protocol=https\nhost=github.com\npath=Spring-Ocean/gitea-cli-so\n\n' | auth_command git -c core.askPass= -c credential.interactive=false credential fill) || return 0
    printf '%s\n' "$auth_reply" | awk '
      /^protocol=https$/ { protocol=1 }
      /^host=github.com$/ { host=1 }
      /^path=/ { path=substr($0,6) }
      /^password=/ { token=substr($0,10) }
      END { if (protocol && host && (path == "" || path == "Spring-Ocean/gitea-cli-so") && token ~ /^[A-Za-z0-9_.-]+$/) printf "%s", token }
    '
}

if [ -z "$version" ]; then
    download_directory=$(mktemp -d "${TMPDIR:-/tmp}/sotea-download.XXXXXX")
    auth_token=''
    if [ "$github_auth" = auto ]; then auth_token=$(github_token); fi
    # 认证仅用于不跟随跳转的固定API，头通过stdin传递而不是进程参数。
    # 仅401可匿名重试一次；403／429保留失败，不轮换凭证或连续重试。
    api_url='https://api.github.com/repos/Spring-Ocean/gitea-cli-so/releases/latest'
    api_status=$(if [ -n "$auth_token" ]; then printf 'header = "Authorization: Bearer %s"\n' "$auth_token"; fi | curl --disable --config - --proto '=https' --tlsv1.2 --silent --show-error --connect-timeout 15 --max-time 60 --max-filesize 2097152 --output "$download_directory/latest.json" --write-out '%{http_code}' "$api_url") || fail 'Unable to reach GitHub Releases.'
    if [ "$api_status" = 401 ] && [ -n "$auth_token" ]; then
        printf '%s\n' 'Warning: GitHub credential was rejected; retrying anonymously once.' >&2
        api_status=$(curl --disable --proto '=https' --tlsv1.2 --silent --show-error --connect-timeout 15 --max-time 60 --max-filesize 2097152 --output "$download_directory/latest.json" --write-out '%{http_code}' "$api_url") || fail 'Unable to reach GitHub Releases.'
    fi
    auth_token=''
    case "$api_status" in
        200) ;;
        403|429) fail 'GitHub API access was denied or rate limited. Retry later, or pass an exact --version to avoid the API query.' ;;
        *) fail 'No stable release could be resolved. Pass an exact --version for a release candidate.' ;;
    esac
    version=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"\]*\)".*/\1/p' "$download_directory/latest.json")
    rm -rf "$download_directory"
    download_directory=''
fi
[ "${#version}" -le 128 ] || fail 'The release tag is too long.'
printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || \
    fail 'Use an exact release tag such as v0.1.0 or v0.1.0-rc.1.'

download_directory=$(mktemp -d "${TMPDIR:-/tmp}/sotea-download.XXXXXX")
folder="sotea-$version-darwin-$architecture"
archive="$folder.tar.gz"
base="$repository/releases/download/$version"
printf 'Downloading %s for macOS %s...\n' "$version" "$architecture"
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --silent --show-error --location \
    --connect-timeout 15 --max-time 300 --max-filesize 134217728 \
    "$base/$archive" --output "$download_directory/$archive" || fail 'Unable to download the release archive.'
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --silent --show-error --location \
    --connect-timeout 15 --max-time 60 --max-filesize 1048576 \
    "$base/SHA256SUMS" --output "$download_directory/SHA256SUMS" || fail 'Unable to download SHA256SUMS.'
expected=$(awk -v name="$archive" '$2 == name { print $1; count++ } END { if (count != 1) exit 1 }' \
    "$download_directory/SHA256SUMS") || fail 'SHA256SUMS must contain exactly one matching archive.'
printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[0-9a-fA-F]{64}$' || fail 'The archive checksum is invalid.'
actual=$(shasum -a 256 "$download_directory/$archive")
actual=${actual%% *}
[ "$(printf '%s' "$expected" | tr 'A-F' 'a-f')" = "$actual" ] || fail 'Checksum verification failed. Nothing was installed.'

# 只读取精确的二进制成员到暂存文件，不把归档路径展开到用户目录。
# 成员必须唯一且为普通文件，避免链接和重复内容被当作可执行程序。
member="$folder/sotea"
tar -tzf "$download_directory/$archive" > "$download_directory/entries" || fail 'The archive is invalid.'
count=$(awk -v name="$member" '$0 == name { count++ } END { print count+0 }' "$download_directory/entries")
[ "$count" = 1 ] || fail 'The archive must contain exactly one expected executable.'
entry=$(tar -tvzf "$download_directory/$archive" "$member") || fail 'Unable to inspect the executable.'
case "$entry" in
    -*) ;;
    *) fail 'The executable must be a regular file.' ;;
esac
mkdir -p "$install_directory"
staged_binary=$(mktemp "$install_directory/.sotea-install.XXXXXX")
tar -xOzf "$download_directory/$archive" "$member" > "$staged_binary" || fail 'Unable to extract the executable.'
chmod 755 "$staged_binary"
reported=$(SOTEA_UPDATE_CHECK=never "$staged_binary" version --no-interactive) || fail 'The executable could not run on this Mac.'
[ "$reported" = "sotea $version" ] || fail 'The executable version does not match the requested release.'
# 硬链接提供同目录的排他提交；并发安装或原有文件不会被覆盖。
# 提交后只清理暂存名称，不删除成功安装的目标。
link "$staged_binary" "$target" || fail 'The destination appeared or could not be created. Nothing was overwritten.'
rm -f "$staged_binary"
staged_binary=''
printf 'Installed %s at %s\n' "$version" "$target"

if [ "$modify_path" = true ] && [ "$install_directory" = "$HOME/.local/bin" ]; then
    profile=''
    case "${SHELL:-}" in
        */zsh) profile="$HOME/.zshrc" ;;
        */bash)
            # bash 只读取首个存在的登录配置；沿用已有文件，避免遮蔽用户 .profile。
            # 三者都不存在时才创建默认的 .bash_profile。
            profile="$HOME/.bash_profile"
            if [ ! -e "$profile" ] && [ -e "$HOME/.bash_login" ]; then
                profile="$HOME/.bash_login"
            fi
            if [ ! -e "$profile" ] && [ -e "$HOME/.profile" ]; then
                profile="$HOME/.profile"
            fi
            ;;
    esac
    if [ -n "$profile" ]; then
        # 写入字面变量引用，由用户下一次启动 shell 时展开。
        # shellcheck disable=SC2016
        path_line='export PATH="$HOME/.local/bin:$PATH"'
        if ! grep -Fqx "$path_line" "$profile" 2>/dev/null; then
            if ! printf '\n# sotea user installation\n%s\n' "$path_line" >> "$profile"; then
                printf 'Warning: Installed successfully, but could not update %s. Add the installation directory to PATH manually.\n' "$profile" >&2
            fi
        fi
    fi
fi
printf 'Open a new terminal, or run "%s" directly. Ensure its directory is on PATH.\n' "$target"
