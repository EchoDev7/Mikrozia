# ==============================================================================
# MIKROZIA — CHR CONTAINER INSTALLER (NO FIREWALL / NO NAT)
# Version: 2.0.0
# Target: MikroTik CHR x86_64, RouterOS 7.25+
# ==============================================================================
# This installer creates ONLY:
#   - an isolated bridge and VETH
#   - persistent mounts and container environment variables
#   - the Mikrozia container object
#
# It NEVER reads, adds, changes, reorders, or removes RouterOS firewall/NAT
# rules. Routing, NAT, port forwarding, and access control are the operator's
# responsibility and are intentionally outside this project.
# ==============================================================================

:local storageBase "containers"
:local imageRef "majlot/mikrozia:v3.11.0-mikrozia.4"
:local instanceTag "[Mikrozia:container]"

:local bridgeName "br-mikrozia"
:local vethName "veth-mikrozia"
:local gatewayCidr "172.16.10.1/24"
:local gatewayIp "172.16.10.1"
:local containerCidr "172.16.10.2/24"

:local dataDir ($storageBase . "/mikrozia/data")
:local certDir ($storageBase . "/mikrozia/cert")
:local rootDir ($storageBase . "/mikrozia/root-v3.11.0-mikrozia.4")
:local tmpDir ($storageBase . "/mikrozia/tmp")

:put ">>> Mikrozia CHR compatibility checks <<<"

:if ([/system/resource/get architecture-name] != "x86_64") do={
    :error "STOP: This beginner installer is only for CHR x86_64."
}

:local containerPackage [/system/package/find where name="container" and disabled=no]
:if ([:len $containerPackage] = 0) do={
    :error "STOP: The matching RouterOS container package is missing or disabled."
}

:local rosVersion [/system/resource/get version]
:local rosBaseVersion $rosVersion
:local firstSpace [:find $rosVersion " "]
:if ([:typeof $firstSpace] != "nil") do={
    :set rosBaseVersion [:pick $rosVersion 0 $firstSpace]
}
:local firstDot [:find $rosBaseVersion "."]
:if ([:typeof $firstDot] = "nil") do={ :error "STOP: Cannot parse RouterOS version." }
:local rosMajor [:tonum [:pick $rosBaseVersion 0 $firstDot]]
:local versionTail [:pick $rosBaseVersion ($firstDot + 1) [:len $rosBaseVersion]]
:local secondDot [:find $versionTail "."]
:local rosMinor 0
:if ([:typeof $secondDot] = "nil") do={
    :set rosMinor [:tonum $versionTail]
} else={
    :set rosMinor [:tonum [:pick $versionTail 0 $secondDot]]
}
:if ($rosMajor < 7 or ($rosMajor = 7 and $rosMinor < 25)) do={
    :error ("STOP: RouterOS 7.25+ is required. Detected: " . $rosVersion)
}

:local containerPackageVersion [/system/package/get ($containerPackage->0) version]
:if ($containerPackageVersion != $rosBaseVersion) do={
    :error ("STOP: Container package " . $containerPackageVersion . " does not match RouterOS " . $rosBaseVersion . ".")
}

:local containerDeviceMode [/system/device-mode/get container]
:if ($containerDeviceMode != true and $containerDeviceMode != "yes") do={
    :error "STOP: Device-mode container is not enabled."
}
:if ([/system/resource/get total-memory] < 2147483648) do={
    :error "STOP: Allocate at least 2 GiB RAM to CHR (4 GiB recommended)."
}

# Refuse to adopt names already owned by another configuration.
:local existingBridge [/interface/bridge/find where name=$bridgeName]
:if ([:len $existingBridge] > 0) do={
    :if ([/interface/bridge/get ($existingBridge->0) comment] != ($instanceTag . " Bridge")) do={
        :error ("STOP: Bridge name already exists and is not owned by Mikrozia: " . $bridgeName)
    }
}
:local existingVeth [/interface/veth/find where name=$vethName]
:if ([:len $existingVeth] > 0) do={
    :if ([/interface/veth/get ($existingVeth->0) comment] != ($instanceTag . " VETH")) do={
        :error ("STOP: VETH name already exists and is not owned by Mikrozia: " . $vethName)
    }
}

# Detect the exact default subnet already assigned elsewhere. For complete
# overlap detection on existing routers, use mikrozia-preflight.rsc + generator.
:foreach addressId in=[/ip/address/find where network="172.16.10.0"] do={
    :local addressInterface [/ip/address/get $addressId interface]
    :if ($addressInterface != $bridgeName) do={
        :error ("STOP: 172.16.10.0/24 is already used by interface " . $addressInterface . ". Use the generator with another subnet.")
    }
}

:put ">>> Creating the container-only network <<<"
:if ([:len $existingBridge] = 0) do={
    /interface/bridge/add name=$bridgeName comment=($instanceTag . " Bridge")
}
:if ([:len [/ip/address/find where address=$gatewayCidr and interface=$bridgeName]] = 0) do={
    /ip/address/add address=$gatewayCidr interface=$bridgeName comment=($instanceTag . " Gateway")
}
:if ([:len $existingVeth] = 0) do={
    /interface/veth/add name=$vethName address=$containerCidr gateway=$gatewayIp comment=($instanceTag . " VETH")
}
:if ([:len [/interface/bridge/port/find where bridge=$bridgeName and interface=$vethName]] = 0) do={
    /interface/bridge/port/add bridge=$bridgeName interface=$vethName comment=($instanceTag . " Bridge Port")
}

:put ">>> Configuring persistent application storage <<<"
:if ([:len [/container/mounts/find where list="MIKROZIA_DATA" and dst="/etc/x-ui"]] = 0) do={
    /container/mounts/add list=MIKROZIA_DATA src=$dataDir dst="/etc/x-ui" comment=($instanceTag . " Data Mount")
}
:if ([:len [/container/mounts/find where list="MIKROZIA_CERT" and dst="/root/cert"]] = 0) do={
    /container/mounts/add list=MIKROZIA_CERT src=$certDir dst="/root/cert" comment=($instanceTag . " Cert Mount")
}

:if ([:len [/container/envs/find where list="MIKROZIA_ENV" and key="XUI_PORT"]] = 0) do={
    /container/envs/add list=MIKROZIA_ENV key=XUI_PORT value="2053" comment=($instanceTag . " Environment")
}
:if ([:len [/container/envs/find where list="MIKROZIA_ENV" and key="HOME"]] = 0) do={
    /container/envs/add list=MIKROZIA_ENV key=HOME value="/root" comment=($instanceTag . " Environment")
}

/container/config/set registry-url="https://registry-1.docker.io" tmpdir=$tmpDir

:put ">>> Pulling the published Mikrozia image from Docker Hub <<<"
:if ([:len [/container/find where comment=($instanceTag . " Container")]] = 0) do={
    /container/add remote-image=$imageRef interface=$vethName root-dir=$rootDir mountlists="MIKROZIA_DATA,MIKROZIA_CERT" envlist=MIKROZIA_ENV dns="1.1.1.1,8.8.8.8" memory-high=512M memory-max=1024M logging=yes start-on-boot=yes auto-restart-interval=10s comment=($instanceTag . " Container")
} else={
    :put "Container object already exists; image import skipped."
}

:put ">>> Container configuration complete <<<"
:put "No RouterOS firewall or NAT rules were created or changed."
:put "Wait for extraction, then run:"
:put "/container/start [find where comment=\"[Mikrozia:container] Container\"]"
:put "Follow logs with: /log/print follow-only where topics~\"container\""
