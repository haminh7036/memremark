#!/usr/bin/env bash
set -euo pipefail

# MemRemark Smart Installer & Hook Patcher

PREFIX="${PREFIX:-$HOME/.local}"
BINDIR="$PREFIX/bin"
SYSTEMD_USER_DIR="${SYSTEMD_USER_DIR:-$HOME/.config/systemd/user}"
GEMINI_HOOKS="${GEMINI_HOOKS:-$HOME/.gemini/config/hooks.json}"
GEMINI_MCP="${GEMINI_MCP:-$HOME/.gemini/config/mcp_config.json}"
CLAUDE_SETTINGS="${CLAUDE_SETTINGS:-$HOME/.claude/settings.json}"
CLAUDE_MCP="${CLAUDE_MCP:-$HOME/.claude/mcp.json}"

CLI_TARGET="all"
DO_UNINSTALL=false
DO_BUILD=true
DO_SERVICE=true

usage() {
    cat <<EOF
Usage: ./install.sh [OPTIONS]

Options:
  --cli=TARGET      Which CLI hooks & MCP servers to patch: 'antigravity-cli', 'claude-code', or 'all' (default: all)
                    Aliases: 'antigravity', 'agy', 'claude'
  --prefix=PATH     Installation prefix for binaries (default: \$HOME/.local)
  --no-build        Skip binary build & install (only patch hooks & MCP servers)
  --no-service      Skip systemd service setup
  --uninstall       Remove memremark binaries, systemd service, and unpatch hooks/MCP servers
  -h, --help        Show this help message

Examples:
  ./install.sh                                # Install everything (binaries, systemd, all hooks & MCP servers)
  ./install.sh --cli=antigravity-cli          # Install & patch only Antigravity CLI hooks & MCP
  ./install.sh --cli=claude-code              # Install & patch only Claude Code hooks & MCP
  ./install.sh --uninstall                   # Remove all binaries, services, hooks, and MCP configs
EOF
    exit 0
}

for arg in "$@"; do
    case "$arg" in
        --cli=*)
            CLI_TARGET="${arg#*=}"
            ;;
        --prefix=*)
            PREFIX="${arg#*=}"
            BINDIR="$PREFIX/bin"
            ;;
        --no-build)
            DO_BUILD=false
            ;;
        --no-service)
            DO_SERVICE=false
            ;;
        --uninstall)
            DO_UNINSTALL=true
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Error: Unknown option '$arg'" >&2
            echo "Run './install.sh --help' for usage." >&2
            exit 1
            ;;
    esac
done

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Python JSON patcher helper
patch_json_py() {
    python3 -c "
import sys, json, os, shutil

action = sys.argv[1]   # 'patch-antigravity', 'patch-claude', 'unpatch-antigravity', 'unpatch-claude', 'patch-mcp', 'unpatch-mcp'
file_path = os.path.expanduser(sys.argv[2])
bindir = os.path.expanduser(sys.argv[3])

if not os.path.exists(file_path):
    if action.startswith('unpatch'):
        print(f'• File does not exist: {file_path}')
        sys.exit(0)
    dir_name = os.path.dirname(file_path)
    if dir_name:
        os.makedirs(dir_name, exist_ok=True)

data = {}
if os.path.exists(file_path):
    try:
        with open(file_path, 'r', encoding='utf-8') as f:
            data = json.load(f)
    except Exception as e:
        print(f'Warning: Could not parse {file_path}, initializing new JSON: {e}', file=sys.stderr)
        data = {}

# Backup before modifying
if os.path.exists(file_path):
    shutil.copy2(file_path, file_path + '.bak')

changed = False

if action == 'patch-antigravity':
    hook_bin = os.path.join(bindir, 'memremark-hook-antigravity-preinvocation')
    memremark_hook = {
        'PreInvocation': [
            {
                'type': 'command',
                'command': hook_bin,
                'timeout': 5
            }
        ]
    }
    data['memremark'] = memremark_hook
    changed = True
    print(f'✓ Patched Antigravity hook into {file_path}')

elif action == 'unpatch-antigravity':
    if 'memremark' in data:
        del data['memremark']
        changed = True
        print(f'✓ Removed memremark hook from {file_path}')
    else:
        print(f'• No memremark hook found in {file_path}')

elif action == 'patch-claude':
    hook_bin = os.path.join(bindir, 'memremark-hook-claude-sessionstart')
    hooks = data.setdefault('hooks', {})
    session_start = hooks.setdefault('SessionStart', [])

    # Check if memremark hook already exists
    exists = False
    for entry in session_start:
        for h in entry.get('hooks', []):
            if 'memremark-hook-claude-sessionstart' in h.get('command', ''):
                exists = True
                h['command'] = hook_bin  # Update path
                break
        if exists:
            break

    if not exists:
        session_start.append({
            'matcher': 'startup|clear|compact',
            'hooks': [
                {
                    'type': 'command',
                    'command': hook_bin
                }
            ]
        })
    changed = True
    print(f'✓ Patched Claude Code hook into {file_path}')

elif action == 'unpatch-claude':
    hooks = data.get('hooks', {})
    session_start = hooks.get('SessionStart', [])
    new_session_start = []
    for entry in session_start:
        is_memremark = False
        for h in entry.get('hooks', []):
            if 'memremark-hook-claude-sessionstart' in h.get('command', ''):
                is_memremark = True
                break
        if not is_memremark:
            new_session_start.append(entry)

    if len(new_session_start) != len(session_start):
        hooks['SessionStart'] = new_session_start
        changed = True
        print(f'✓ Removed memremark hook from {file_path}')
    else:
        print(f'• No memremark hook found in {file_path}')

elif action == 'patch-mcp':
    mcp_bin = os.path.join(bindir, 'memremark-mcp')
    servers = data.setdefault('mcpServers', {})
    servers['memremark'] = {
        'command': mcp_bin
    }
    changed = True
    print(f'✓ Patched MemRemark MCP server into {file_path}')

elif action == 'unpatch-mcp':
    servers = data.get('mcpServers', {})
    if 'memremark' in servers:
        del servers['memremark']
        changed = True
        print(f'✓ Removed MemRemark MCP server from {file_path}')
    else:
        print(f'• No MemRemark MCP server found in {file_path}')

if changed:
    with open(file_path, 'w', encoding='utf-8') as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
        f.write('\n')
" "$@"
}

if [ "$DO_UNINSTALL" = true ]; then
    echo "=== Uninstalling MemRemark ==="
    
    # 1. Stop & remove systemd service
    if command -v systemctl &>/dev/null; then
        echo "Stopping systemd service..."
        systemctl --user stop memremarkd.service 2>/dev/null || true
        systemctl --user disable memremarkd.service 2>/dev/null || true
        rm -f "$SYSTEMD_USER_DIR/memremarkd.service"
        systemctl --user daemon-reload 2>/dev/null || true
    fi

    # 2. Unpatch hooks & MCP servers
    patch_json_py "unpatch-antigravity" "$GEMINI_HOOKS" "$BINDIR"
    patch_json_py "unpatch-mcp" "$GEMINI_MCP" "$BINDIR"
    patch_json_py "unpatch-claude" "$CLAUDE_SETTINGS" "$BINDIR"
    patch_json_py "unpatch-mcp" "$CLAUDE_MCP" "$BINDIR"

    # 3. Remove binaries
    rm -f "$BINDIR/memremarkd" \
          "$BINDIR/memremark-hook-claude-sessionstart" \
          "$BINDIR/memremark-hook-antigravity-preinvocation" \
          "$BINDIR/memremark-mcp"
    
    echo "✓ MemRemark successfully uninstalled."
    exit 0
fi

echo "=== Installing MemRemark ==="

# 1. Build & install binaries
if [ "$DO_BUILD" = true ]; then
    echo "Building binaries..."
    cd "$REPO_DIR"
    mkdir -p "$REPO_DIR/bin" "$BINDIR"
    
    go build -o "$REPO_DIR/bin/memremarkd" ./cmd/memremarkd
    go build -o "$REPO_DIR/bin/memremark-hook-claude-sessionstart" ./cmd/memremark-hook-claude-sessionstart
    go build -o "$REPO_DIR/bin/memremark-hook-antigravity-preinvocation" ./cmd/memremark-hook-antigravity-preinvocation
    go build -o "$REPO_DIR/bin/memremark-mcp" ./cmd/memremark-mcp

    install -m 755 "$REPO_DIR/bin/memremarkd" "$BINDIR/memremarkd"
    install -m 755 "$REPO_DIR/bin/memremark-hook-claude-sessionstart" "$BINDIR/memremark-hook-claude-sessionstart"
    install -m 755 "$REPO_DIR/bin/memremark-hook-antigravity-preinvocation" "$BINDIR/memremark-hook-antigravity-preinvocation"
    install -m 755 "$REPO_DIR/bin/memremark-mcp" "$BINDIR/memremark-mcp"
    echo "✓ Binaries installed to $BINDIR"
fi

# 2. Systemd service setup
if [ "$DO_SERVICE" = true ] && command -v systemctl &>/dev/null; then
    mkdir -p "$SYSTEMD_USER_DIR"
    install -m 644 "$REPO_DIR/systemd/memremarkd.service" "$SYSTEMD_USER_DIR/memremarkd.service"
    systemctl --user daemon-reload
    systemctl --user enable --now memremarkd.service
    echo "✓ Systemd service 'memremarkd.service' enabled and started."
fi

# 3. Patch hooks & MCP configuration based on --cli target
case "$CLI_TARGET" in
    antigravity|antigravity-cli|agy)
        patch_json_py "patch-antigravity" "$GEMINI_HOOKS" "$BINDIR"
        patch_json_py "patch-mcp" "$GEMINI_MCP" "$BINDIR"
        ;;
    claude|claude-code)
        patch_json_py "patch-claude" "$CLAUDE_SETTINGS" "$BINDIR"
        patch_json_py "patch-mcp" "$CLAUDE_MCP" "$BINDIR"
        ;;
    all)
        patch_json_py "patch-antigravity" "$GEMINI_HOOKS" "$BINDIR"
        patch_json_py "patch-mcp" "$GEMINI_MCP" "$BINDIR"
        patch_json_py "patch-claude" "$CLAUDE_SETTINGS" "$BINDIR"
        patch_json_py "patch-mcp" "$CLAUDE_MCP" "$BINDIR"
        ;;
    *)
        echo "Error: Unknown CLI target '$CLI_TARGET'. Use 'antigravity-cli', 'claude-code', or 'all'." >&2
        exit 1
        ;;
esac

echo ""
echo "🎉 MemRemark setup complete!"
echo "Daemon status: systemctl --user status memremarkd"
