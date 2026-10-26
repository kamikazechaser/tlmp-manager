#!/bin/sh
# Install or upgrade tlmp-manager: curl -fsSL <url>/install.sh | sh
# Options: TLMP_VERSION=v1.2.3, TLMP_BIN_DIR=dir, TLMP_NO_SETUP=1, TLMP_BASE_URL=mirror
set -eu

repo=kamikazechaser/tlmp-manager
version=${TLMP_VERSION:-latest}
bin_dir=${TLMP_BIN_DIR:-$HOME/.local/bin}
asset=tlmp-manager-linux-amd64

if [ "$(uname -s)" != Linux ] || [ "$(uname -m)" != x86_64 ]; then
	echo "tlmp-manager supports Linux x86_64 only" >&2
	exit 1
fi

if [ "$version" = latest ]; then
	base=${TLMP_BASE_URL:-https://github.com/$repo/releases/latest/download}
else
	base=${TLMP_BASE_URL:-https://github.com/$repo/releases/download/$version}
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading tlmp-manager $version"
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt"
(cd "$tmp" && grep "  $asset\$" checksums.txt | sha256sum -c --quiet -)

mkdir -p "$bin_dir"
install -m 755 "$tmp/$asset" "$bin_dir/.tlmp-manager.new"
mv -f "$bin_dir/.tlmp-manager.new" "$bin_dir/tlmp-manager"
echo "Installed $bin_dir/tlmp-manager"

case ":$PATH:" in
*":$bin_dir:"*) ;;
*)
	shell=$(basename "${SHELL:-sh}")
	case $shell in
	zsh) rc=$HOME/.zshrc ;;
	bash) rc=$HOME/.bashrc ;;
	*) rc=$HOME/.profile ;;
	esac
	if [ "$shell" = fish ]; then
		fish -c "fish_add_path -U '$bin_dir'"
		echo "Added $bin_dir to the fish PATH. Open a new terminal to use tlmp-manager"
	elif ! grep -qsF "$bin_dir" "$rc"; then
		printf "\nexport PATH=\"%s:\$PATH\"\n" "$bin_dir" >>"$rc"
		echo "Added $bin_dir to PATH in $rc. Open a new terminal to use tlmp-manager"
	fi
	;;
esac

if [ -z "${TLMP_NO_SETUP:-}" ]; then
	"$bin_dir/tlmp-manager" setup
fi
