#!/bin/bash

set -e

REPO="LuckfoxTECH/luckclaw"
INSTALL_DIR="/usr/bin"
LUCKCLAW_HOME="${LUCKCLAW_HOME:-$HOME/.luckclaw}"
FORCE_VERSION=""
USE_SUDO=false

check_install_method() {
    if [ "$(id -u)" = "0" ]; then
        echo "Running as root, installing to /usr/bin"
        USE_SUDO=false
        INSTALL_DIR="/usr/bin"
        return 0
    fi
    
    if ! command -v sudo &> /dev/null; then
        echo "Warning: sudo not found, will install to ~/.local/bin"
        USE_SUDO=false
        INSTALL_DIR="$HOME/.local/bin"
        return 0
    fi
    
    if sudo -n true 2>/dev/null; then
        echo "sudo available, installing to /usr/bin"
        USE_SUDO=true
        INSTALL_DIR="/usr/bin"
        return 0
    else
        echo "Warning: No sudo access, will install to ~/.local/bin"
        USE_SUDO=false
        INSTALL_DIR="$HOME/.local/bin"
        return 0
    fi
}

usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -v, --version VERSION    Specify version to install (default: latest)"
    echo "  -d, --install-dir DIR    Install to custom directory (default: /usr/bin)"
    echo "  -h, --help               Show this help message"
    echo ""
}

while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--version)
            FORCE_VERSION="$2"
            shift 2
            ;;
        -d|--install-dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

detect_os() {
    case "$(uname -s)" in
        Linux*)     echo "linux" ;;
        Darwin*)    echo "darwin" ;;
        *)          echo "unsupported" ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "x86_64" ;;
        aarch64|arm64)  echo "arm64" ;;
        armv7|armv7l)   echo "armv7" ;;
        *)              echo "unsupported" ;;
    esac
}

get_version() {
    if [ -n "$FORCE_VERSION" ]; then
        echo "$FORCE_VERSION"
    else
        curl -sL "https://api.github.com/repos/$REPO/releases/latest" | grep -o '"tag_name": "v[^"]*"' | cut -d'"' -f4 | sed 's/v//'
    fi
}

download_binary() {
    local version="$1"
    local os="$2"
    local arch="$3"
    local filename="luckclaw-${os}-${arch}"
    local url="https://github.com/$REPO/releases/download/v${version}/${filename}"

    echo "Downloading luckclaw v${version} for ${os}-${arch}..."

    if curl -#L --fail "$url" -o "/tmp/${filename}"; then
        echo "Downloaded to /tmp/${filename}"
    else
        echo "Error: Failed to download from $url"
        echo "Please check if the release is available for your platform"
        exit 1
    fi
}

check_dependencies() {
    local missing=""

    if ! command -v curl &> /dev/null; then
        missing="$missing curl"
    fi

    if [ -n "$missing" ]; then
        echo "Error: Missing dependencies:$missing"
        echo "Please install them first"
        exit 1
    fi
}

add_to_path() {
    local bin_dir="$1"
    local shell_rc=""

    if [ -n "$FORCE_VERSION" ]; then
        return
    fi

    if [ "$bin_dir" = "/usr/bin" ]; then
        return
    fi

    local current_shell=""
    current_shell="$(ps -p $$ -o comm= 2>/dev/null)"

    if [ -z "$current_shell" ]; then
        if [ -n "$ZSH_VERSION" ]; then
            current_shell="zsh"
        elif [ -n "$BASH_VERSION" ]; then
            current_shell="bash"
        fi
    fi

    case ":$PATH:" in
        *:${bin_dir}:*)
            echo "${bin_dir} already in PATH"
            return
            ;;
    esac

    if [ "$current_shell" = "zsh" ]; then
        shell_rc="$HOME/.zshrc"
    elif [ "$current_shell" = "bash" ]; then
        if [ -f "$HOME/.bashrc" ]; then
            shell_rc="$HOME/.bashrc"
        elif [ -f "$HOME/.bash_profile" ]; then
            shell_rc="$HOME/.bash_profile"
        fi
    fi

    if [ -n "$shell_rc" ]; then
        if grep -q "${bin_dir}" "$shell_rc" 2>/dev/null; then
            echo "${bin_dir} already configured in $shell_rc, but not in current PATH"
            echo "Please run: source $shell_rc"
        else
            echo "" >> "$shell_rc"
            echo "# Added by luckclaw installer" >> "$shell_rc"
            echo "export PATH=\"${bin_dir}:\$PATH\"" >> "$shell_rc"
            echo "Added ${bin_dir} to PATH in $shell_rc"
            echo "Please run: source $shell_rc"
        fi
    else
        echo "Warning: Unable to detect shell config file"
        echo "Please add the following line to your shell config file:"
        echo "  export PATH=\"${bin_dir}:\$PATH\""
    fi
}

main() {
    echo "🍀 luckclaw Installer"
    echo "====================="
    echo ""

    check_dependencies

    local os=$(detect_os)
    local arch=$(detect_arch)

    if [ "$os" = "unsupported" ]; then
        echo "Error: Unsupported operating system"
        exit 1
    fi

    if [ "$arch" = "unsupported" ]; then
        echo "Error: Unsupported architecture: $(uname -m)"
        exit 1
    fi

    echo "Detected: ${os}-${arch}"

    local version=$(get_version)
    echo "Version: ${version}"
    echo ""

    download_binary "$version" "$os" "$arch"

    local filename="luckclaw-${os}-${arch}"
    
    check_install_method

    if [ ! -d "$INSTALL_DIR" ]; then
        echo "Creating install directory: $INSTALL_DIR"
        if [ "$USE_SUDO" = true ]; then
            sudo mkdir -p "$INSTALL_DIR" || { echo "Error: Failed to create $INSTALL_DIR"; exit 1; }
        else
            mkdir -p "$INSTALL_DIR" || { echo "Error: Failed to create $INSTALL_DIR"; exit 1; }
        fi
    fi

    echo "Installing to $INSTALL_DIR..."
    if [ "$USE_SUDO" = true ]; then
        sudo mv "/tmp/${filename}" "$INSTALL_DIR/luckclaw" || { echo "Error: Failed to move binary"; exit 1; }
        sudo chmod +x "$INSTALL_DIR/luckclaw" || { echo "Error: Failed to set permissions"; exit 1; }
    else
        mv "/tmp/${filename}" "$INSTALL_DIR/luckclaw" || { echo "Error: Failed to move binary"; exit 1; }
        chmod +x "$INSTALL_DIR/luckclaw" || { echo "Error: Failed to set permissions"; exit 1; }
    fi

    echo "Installed luckclaw to $INSTALL_DIR/luckclaw"
    echo ""

    add_to_path "$INSTALL_DIR"

    echo "Setting up luckclaw configuration..."
    mkdir -p "$LUCKCLAW_HOME"

    if [ ! -f "$LUCKCLAW_HOME/config.json" ]; then
        echo "Running initial setup (luckclaw onboard)..."
        echo ""

        if "$INSTALL_DIR/luckclaw" onboard; then
            echo ""
            echo "✅ Initial setup completed!"
        else
            echo ""
            echo "⚠️  Initial setup encountered issues, but you can run 'luckclaw onboard' later"
        fi
    else
        echo "Configuration already exists at $LUCKCLAW_HOME/config.json"
    fi

    echo ""
    echo "====================="
    echo "🎉 Installation complete!"
    echo ""
    echo "Next steps:"
    echo "  1. Make sure $INSTALL_DIR is in your PATH"
    echo "  2. Configure your API key in $LUCKCLAW_HOME/config.json"
    echo "  3. Run 'luckclaw --help' to get started"
    echo ""
    echo "Useful commands:"
    echo "  luckclaw config        - Configure API keys and settings"
    echo "  luckclaw onboard       - Run interactive setup wizard"
    echo "  luckclaw agent         - Start interactive chat"
    echo ""
}

main
