# ==============================================================================
# MIKROZIA — READ-ONLY ROUTEROS PREFLIGHT INSPECTOR
# ==============================================================================
# Version: 1.2.0
# Mode: STRICTLY READ-ONLY (Makes ZERO configuration or filesystem changes)
# Purpose: Gathers hardware, storage, memory, and network facts and emits
#          a structured, machine-readable JSON payload for the configuration
#          generator.
# ==============================================================================

:put "=================================================================="

# RouterOS has no C-style ternary operator. Keep JSON conversion explicit and
# escape every string originating from RouterOS before placing it in JSON.
:local jsonBool do={
    :if ($1 = true or $1 = "true" or $1 = "yes") do={ :return "true" }
    :return "false"
}

:local jsonEscape do={
    :local value [:tostr $1]
    :local escaped ""
    :local valueLength [:len $value]
    :if ($valueLength = 0) do={ :return "" }
    :for idx from=0 to=($valueLength - 1) do={
        :local ch [:pick $value $idx ($idx + 1)]
        :if ($ch = "\\") do={
            :set escaped ($escaped . "\\\\")
        } else={
            :if ($ch = "\"") do={
                :set escaped ($escaped . "\\\"")
            } else={
                :if ($ch = "\n") do={
                    :set escaped ($escaped . "\\n")
                } else={
                    :if ($ch = "\r") do={
                        :set escaped ($escaped . "\\r")
                    } else={
                        :if ($ch = "\t") do={
                            :set escaped ($escaped . "\\t")
                        } else={
                            :set escaped ($escaped . $ch)
                        }
                    }
                }
            }
        }
    }
    :return $escaped
}
:put ">>> GATHERING MIKROTIK ROUTEROS FACTS (READ-ONLY) <<<"
:put "=================================================================="

# 1. System Hardware & Memory
:local rosVersion [/system/resource get version]
:local rosArch [/system/resource get architecture-name]
:local boardName [/system/resource get board-name]
:local totalMem [/system/resource get total-memory]
:local freeMem [/system/resource get free-memory]
:local totalHdd [/system/resource get total-hdd-space]
:local freeHdd [/system/resource get free-hdd-space]

# 2. Container Subsystem & Device Mode
:local pkgInstalled false
:local pkgEnabled false
:local pkgVersion ""
:foreach p in=[/system/package find where name="container"] do={
    :set pkgInstalled true
    :set pkgVersion [/system/package get $p version]
    :if (![/system/package get $p disabled]) do={
        :set pkgEnabled true
    }
}

:local devModeContainer false
:do {
    :local dm [/system/device-mode get container]
    :if ($dm = true or $dm = "yes") do={
        :set devModeContainer true
    }
} on-error={
    :set devModeContainer false
}

# 3. Output Machine-Readable JSON
:put ""
:put "--- BEGIN MIKROZIA PREFLIGHT JSON ---"
:put "{"
:put "  \"schema_version\": \"1.0.0\","
:put ("  \"system\": {")
:put ("    \"version\": \"" . [$jsonEscape $rosVersion] . "\",")
:put ("    \"architecture\": \"" . [$jsonEscape $rosArch] . "\",")
:put ("    \"board_name\": \"" . [$jsonEscape $boardName] . "\",")
:put ("    \"total_memory_bytes\": " . $totalMem . ",")
:put ("    \"free_memory_bytes\": " . $freeMem . ",")
:put ("    \"total_hdd_space_bytes\": " . $totalHdd . ",")
:put ("    \"free_hdd_space_bytes\": " . $freeHdd)
:put "  },"
:put "  \"subsystems\": {"
:put ("    \"container_package_installed\": " . [$jsonBool $pkgInstalled] . ",")
:put ("    \"container_package_enabled\": " . [$jsonBool $pkgEnabled] . ",")
:put ("    \"container_package_version\": \"" . [$jsonEscape $pkgVersion] . "\",")
:put ("    \"device_mode_container_enabled\": " . [$jsonBool $devModeContainer])
:put "  },"

# Disks (including CHR virtual disk fallback)
:put "  \"storage\": ["
:local firstDisk true
:if ([:len [/disk find]] = 0) do={
    :put ("    {\"name\": \"system\", \"type\": \"virtual_disk\", \"total_bytes\": " . $totalHdd . ", \"free_bytes\": " . $freeHdd . "}")
    :set firstDisk false
} else={
    :foreach d in=[/disk find] do={
        :local dName [/disk get $d name]
        :local dType [/disk get $d type]
        :local dSize [/disk get $d size]
        :local dFree [/disk get $d free]
        :if (!$firstDisk) do={ :put "," }
        :put ("    {\"name\": \"" . [$jsonEscape $dName] . "\", \"type\": \"" . [$jsonEscape $dType] . "\", \"total_bytes\": " . $dSize . ", \"free_bytes\": " . $dFree . "}")
        :set firstDisk false
    }
}
:put "  ],"

# Network Interfaces
:put "  \"network\": {"
:put "    \"interfaces\": ["
:local firstIf true
:foreach i in=[/interface find] do={
    :local iName [/interface get $i name]
    :local iType [/interface get $i type]
    :local iDis [/interface get $i disabled]
    :if (!$firstIf) do={ :put "," }
    :put ("      {\"name\": \"" . [$jsonEscape $iName] . "\", \"type\": \"" . [$jsonEscape $iType] . "\", \"disabled\": " . [$jsonBool $iDis] . "}")
    :set firstIf false
}
:put "    ],"

# Interface Lists
:put "    \"interface_lists\": {"
:local firstList true
:foreach l in=[/interface/list find] do={
    :local lName [/interface/list get $l name]
    :if (!$firstList) do={ :put "," }
    :put ("      \"" . [$jsonEscape $lName] . "\": [")
    :local firstMem true
    :foreach m in=[/interface/list/member find where list=$lName] do={
        :local mIf [/interface/list/member get $m interface]
        :if (!$firstMem) do={ :put "," }
        :put ("        \"" . [$jsonEscape $mIf] . "\"")
        :set firstMem false
    }
    :put "      ]"
    :set firstList false
}
:put "    },"

# IP Addresses
:put "    \"ip_addresses\": ["
:local firstAddr true
:foreach a in=[/ip/address find] do={
    :local aAddr [/ip/address get $a address]
    :local aNet [/ip/address get $a network]
    :local aIf [/ip/address get $a interface]
    :local aDis [/ip/address get $a disabled]
    :if (!$firstAddr) do={ :put "," }
    :put ("      {\"address\": \"" . [$jsonEscape $aAddr] . "\", \"network\": \"" . [$jsonEscape $aNet] . "\", \"interface\": \"" . [$jsonEscape $aIf] . "\", \"disabled\": " . [$jsonBool $aDis] . "}")
    :set firstAddr false
}
:put "    ],"

# IP Routes
:put "    \"ip_routes\": ["
:local firstRoute true
:foreach r in=[/ip/route find] do={
    :local rDst [/ip/route get $r dst-address]
    :local rDis [/ip/route get $r disabled]
    :if (!$firstRoute) do={ :put "," }
    :put ("      {\"dst_address\": \"" . [$jsonEscape $rDst] . "\", \"disabled\": " . [$jsonBool $rDis] . "}")
    :set firstRoute false
}
:put "    ],"

# Bridges
:put "    \"bridges\": ["
:local firstBr true
:foreach b in=[/interface/bridge find] do={
    :local bName [/interface/bridge get $b name]
    :local bDis [/interface/bridge get $b disabled]
    :local bComment ""
    :do { :set bComment [/interface/bridge get $b comment] } on-error={}
    :if (!$firstBr) do={ :put "," }
    :put ("      {\"name\": \"" . [$jsonEscape $bName] . "\", \"comment\": \"" . [$jsonEscape $bComment] . "\", \"disabled\": " . [$jsonBool $bDis] . "}")
    :set firstBr false
}
:put "    ],"

# VETHs
:put "    \"veths\": ["
:local firstVeth true
:foreach v in=[/interface/veth find] do={
    :local vName [/interface/veth get $v name]
    :local vDis [/interface/veth get $v disabled]
    :local vComment ""
    :do { :set vComment [/interface/veth get $v comment] } on-error={}
    :if (!$firstVeth) do={ :put "," }
    :put ("      {\"name\": \"" . [$jsonEscape $vName] . "\", \"comment\": \"" . [$jsonEscape $vComment] . "\", \"disabled\": " . [$jsonBool $vDis] . "}")
    :set firstVeth false
}
:put "    ]"
:put "  },"

# Existing Containers
:put "  \"existing_containers\": {"
:put "    \"containers\": ["
:local firstCnt true
:foreach c in=[/container find] do={
    :local cRoot ""
    :do { :set cRoot [/container get $c root-dir] } on-error={}
    :local cName ""
    :do { :set cName [/container get $c name] } on-error={}
    :local cComment ""
    :do { :set cComment [/container get $c comment] } on-error={}
    :if (!$firstCnt) do={ :put "," }
    :put ("      {\"root_dir\": \"" . [$jsonEscape $cRoot] . "\", \"name\": \"" . [$jsonEscape $cName] . "\", \"comment\": \"" . [$jsonEscape $cComment] . "\"}")
    :set firstCnt false
}
:put "    ]"
:put "  }"
:put "}"
:put "--- END MIKROZIA PREFLIGHT JSON ---"
:put "=================================================================="
:put ">>> PREFLIGHT DISCOVERY COMPLETE (Zero changes made) <<<"
:put "=================================================================="
