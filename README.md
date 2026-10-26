# tlmp-manager

[![Tag](https://img.shields.io/github/v/tag/kamikazechaser/tlmp-manager?sort=semver)](https://github.com/kamikazechaser/tlmp-manager/tags)
[![License](https://img.shields.io/github/license/kamikazechaser/tlmp-manager)](LICENSE)

tlmp-manager is a simple CLI-based Linux launcher for [TrucklineMP](https://trucklinemp.com/).

Set it up once, then just press Play in Steam. Updates and authentication are handled automatically, with no background services or extra windows to keep open.

> ⚠️ tlmp-manager is in early development. It may not work on all systems.

## Requirements

- ETS2 on Steam for Linux
- Proton Experimental

To set Proton Experimental: `Steam > ETS2 > Properties > Compatibility > Force the use of a specific Steam Play compatibility tool`

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/kamikazechaser/tlmp-manager/master/install.sh | sh
```

The script installs `tlmp-manager` in `~/.local/bin`, then runs `tlmp-manager setup`. Run the script again to upgrade.

### Build

Requirements: Latest Go and `x86_64-w64-mingw32-gcc`.

```sh
make build
make test
```

## Use

1. Run `tlmp-manager`. It automatically finds ETS2, logs in to TrucklineMP, and installs TrucklineMP.
2. Set the ETS2 launch options in Steam to the value that `tlmp-manager` shows.
3. Start ETS2 from Steam with the default Launch option (DirectX 11 is recommended).

### Switching between multiplayer mods or vanilla ETS2

1. Run `tlmp-manager disable`.
2. In Steam > ETS2 > Properties, remove the TrucklineMP launch options and set the compatibility tool, if any, that the other mod uses. For example, SteamTinkerLauncher.

To play TrucklineMP again, set Proton Experimental and the tlmp-manager launch options. The next start turns TrucklineMP on.

Other commands: `status`, `disable`, `logout` and `uninstall`. See `tlmp-manager --help`. Logs are in `~/.local/state/tlmp-manager/logs`.

## Acknowledgements

Thanks to the TrucklineMP developers for their work on the mod and for the changes and documentation that helped make first-class Linux support possible.

## License

[AGPL-3.0](LICENSE)
