# ==============================================================================
# MIKROZIA — SAFE CONTAINER-ONLY ROLLBACK
# Version: 2.0.0
# ==============================================================================
# Removes only Mikrozia container, mount/env definitions, and its bridge/VETH.
# Persistent database and certificates are preserved.
# Firewall/NAT is never inspected or modified.
# ==============================================================================

:local instanceTag "[Mikrozia:container]"

:put ">>> Stopping owned Mikrozia container <<<"
:do {
    /container/stop [find where comment=($instanceTag . " Container")]
    :delay 3s
} on-error={}

:put ">>> Removing owned container object and container-only resources <<<"
:do { /container/remove [find where comment=($instanceTag . " Container")] } on-error={}
:do { /container/mounts/remove [find where comment=($instanceTag . " Data Mount")] } on-error={}
:do { /container/mounts/remove [find where comment=($instanceTag . " Cert Mount")] } on-error={}
:do { /container/envs/remove [find where comment=($instanceTag . " Environment")] } on-error={}

:do { /interface/bridge/port/remove [find where comment=($instanceTag . " Bridge Port")] } on-error={}
:do { /ip/address/remove [find where comment=($instanceTag . " Gateway")] } on-error={}
:do { /interface/veth/remove [find where comment=($instanceTag . " VETH")] } on-error={}
:do { /interface/bridge/remove [find where comment=($instanceTag . " Bridge")] } on-error={}

:put ">>> Safe rollback complete <<<"
:put "No firewall/NAT rules were inspected or modified."
:put "Database and certificates remain under containers/mikrozia/."
