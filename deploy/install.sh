#!/bin/sh
# Install the binary, the persistent directories, the environment files and the
# systemd units on the Pi. It is idempotent: re-running it replaces the binary
# and the units, never the database, the objects or an existing env file.
set -eu

prefix=${CONTRATOS_PREFIX:-/opt/la-mordida}
state=${CONTRATOS_STATE:-/var/lib/la-mordida}
backups=${CONTRATOS_BACKUP:-/var/backups/la-mordida}
config=${CONTRATOS_CONFIG:-/etc/la-mordida}
user=${CONTRATOS_USER:-contratos}
binary=${CONTRATOS_BINARY:-bin/contratos-linux-arm64}
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
    echo "install: run me as root on the Pi" >&2
    exit 1
fi
if [ ! -f "$binary" ]; then
    echo "install: binary $binary does not exist; run make build-pi first" >&2
    exit 1
fi
if ! file "$binary" | grep -q 'ARM aarch64'; then
    echo "install: $binary is not an aarch64 binary" >&2
    file "$binary" >&2
    exit 1
fi

if ! id "$user" >/dev/null 2>&1; then
    useradd --system --home "$state" --shell /usr/sbin/nologin "$user"
fi

install -d -m 0755 "$prefix"
install -d -o "$user" -g "$user" -m 0750 "$state" "$state/objects"
install -d -o "$user" -g "$user" -m 0700 "$backups"
install -d -m 0755 "$config" "$config/channels"

install -m 0755 -o root -g root "$binary" "$prefix/contratos"
for script in backup.sh restore.sh report.sh load-history.sh; do
    install -m 0755 -o root -g root "$here/$script" "$prefix/$script"
done
install -m 0644 -o root -g root "$here/README.md" "$prefix/README.md"

if [ ! -f "$config/contratos.env" ]; then
    install -m 0644 -o root -g root "$here/contratos.env.example" "$config/contratos.env"
    echo "install: wrote $config/contratos.env (edit the address or paths if needed)"
else
    echo "install: kept existing $config/contratos.env"
fi
for channel in "$here"/channels/*.env; do
    install -m 0644 -o root -g root "$channel" "$config/channels/$(basename "$channel")"
done

install -m 0644 -o root -g root "$here"/systemd/*.service "$here"/systemd/*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now contratos-serve.service
systemctl enable --now contratos-archive.timer contratos-backup.timer

if [ "${ENABLE_IMPORT:-0}" -eq 1 ]; then
    for channel in "$config"/channels/*.env; do
        name=$(basename "$channel" .env)
        systemctl enable --now "contratos-import@$name.timer"
    done
else
    echo "install: import timers are installed but not enabled; bootstrap the first window, then:"
    echo "  systemctl enable --now contratos-import@sindicacion_643.timer"
fi

systemctl --no-pager --full status contratos-serve.service | sed -n '1,12p' || true
echo "install: state in $state, backups in $backups"
