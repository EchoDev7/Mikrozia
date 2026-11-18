#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
plain='\033[0m'

#Add some basic function here
function LOGD() {
    echo -e "${yellow}[DEG] $* ${plain}"
}

function LOGE() {
    echo -e "${red}[ERR] $* ${plain}"
}

function LOGI() {
    echo -e "${green}[INF] $* ${plain}"
}

# Port helpers: detect listener and owning process (best effort)
is_port_in_use() {
    local port="$1"
    if command -v ss > /dev/null 2>&1; then
        ss -ltn 2> /dev/null | awk -v p=":${port}$" '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v netstat > /dev/null 2>&1; then
        netstat -lnt 2> /dev/null | awk -v p=":${port} " '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v lsof > /dev/null 2>&1; then
        lsof -nP -iTCP:${port} -sTCP:LISTEN > /dev/null 2>&1 && return 0
    fi
    return 1
}

# check root
[[ $EUID -ne 0 ]] && LOGE "ERROR: You must be root to run this script! \n" && exit 1

# Check OS and set release variable
if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    echo "Failed to check the system OS, please contact the author!" >&2
    exit 1
fi
echo "The OS release is: $release"

os_version=""
os_version=$(grep "^VERSION_ID" /etc/os-release | cut -d '=' -f2 | tr -d '"' | tr -d '.')

running_in_docker="false"
if [[ -f /.dockerenv ]] || [[ "${XUI_IN_DOCKER}" == "true" ]]; then
    running_in_docker="true"
fi

# Declare Variables
if [[ "${running_in_docker}" == "true" ]]; then
    xui_folder="${XUI_MAIN_FOLDER:=/app}"
else
    xui_folder="${XUI_MAIN_FOLDER:=/usr/local/x-ui}"
fi
xui_service="${XUI_SERVICE:=/etc/systemd/system}"
log_folder="${XUI_LOG_FOLDER:=/var/log/x-ui}"
mkdir -p "${log_folder}"

confirm() {
    if [[ $# -gt 1 ]]; then
        echo && read -rp "$1 [Default $2]: " temp
        if [[ "${temp}" == "" ]]; then
            temp=$2
        fi
    else
        read -rp "$1 [y/n]: " temp
    fi
    if [[ "${temp}" == "y" || "${temp}" == "Y" ]]; then
        return 0
    else
        return 1
    fi
}

confirm_restart() {
    confirm "Restart the panel, Attention: Restarting the panel will also restart xray" "y"
    if [[ $? == 0 ]]; then
        restart
    else
        show_menu
    fi
}

before_show_menu() {
    echo && echo -n -e "${yellow}Press enter to return to the main menu: ${plain}" && read -r temp
    show_menu
}

xui_env_file_path() {
    case "${release}" in
        ubuntu | debian | armbian)
            echo "/etc/default/x-ui"
            ;;
        arch | manjaro | parch | alpine)
            echo "/etc/conf.d/x-ui"
            ;;
        *)
            echo "/etc/sysconfig/x-ui"
            ;;
    esac
}

admin_credential_menu() {
    echo -e "${yellow}The panel administrator account belongs to the RouterOS Container Environment.${plain}"
    echo -e "${yellow}Mikrozia never prints a credential and never generates one.${plain}"
    echo ""
    if [[ -n "${MIKROZIA_ADMIN_USERNAME:-}" && -n "${MIKROZIA_ADMIN_PASSWORD:-}" ]]; then
        echo -e "${green}Status:${plain} the administrator account is managed by the container environment."
        echo -e "        Changing the environment values and restarting the container reconciles the stored account."
    elif [[ -n "${MIKROZIA_ADMIN_USERNAME:-}" || -n "${MIKROZIA_ADMIN_PASSWORD:-}" ]]; then
        echo -e "${red}Status:${plain} only one of the two environment values is set."
        echo -e "        The panel refuses to start until both MIKROZIA_ADMIN_USERNAME and MIKROZIA_ADMIN_PASSWORD are set."
    else
        echo -e "${yellow}Status:${plain} the environment does not configure an administrator account."
        echo -e "        The stored account is preserved unchanged so you keep access to the panel."
    fi
    echo ""
    echo -e "${blue}Set the administrator account in RouterOS (Terminal or WinBox):${plain}"
    echo -e "  ${green}/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_USERNAME value=\"YOUR_ADMIN_NAME\" comment=\"[Mikrozia:container] Environment\"${plain}"
    echo -e "  ${green}/container/envs/add list=MIKROZIA_ENV key=MIKROZIA_ADMIN_PASSWORD value=\"YOUR_STRONG_PASSWORD\" comment=\"[Mikrozia:container] Environment\"${plain}"
    echo ""
    echo -e "${blue}Change it later with:${plain}"
    echo -e "  ${green}/container/envs/set [find where list=MIKROZIA_ENV and key=MIKROZIA_ADMIN_USERNAME] value=\"YOUR_ADMIN_NAME\"${plain}"
    echo -e "  ${green}/container/envs/set [find where list=MIKROZIA_ENV and key=MIKROZIA_ADMIN_PASSWORD] value=\"YOUR_STRONG_PASSWORD\"${plain}"
    echo ""
    echo -e "${yellow}After the change, restart the container so the new environment reaches the panel:${plain}"
    echo -e "  ${green}/container/restart [find where comment=\"[Mikrozia:container] Container\"]${plain}"
    echo ""
    echo -e "${yellow}The RouterOS Environment is a configuration store, not a secret store. Anyone who can read the${plain}"
    echo -e "${yellow}RouterOS configuration can read the administrator password.${plain}"
    before_show_menu
}

gen_random_string() {
    local length="$1"
    openssl rand -base64 $((length * 2)) \
        | tr -dc 'a-zA-Z0-9' \
        | head -c "$length"
}

reset_webbasepath() {
    echo -e "${yellow}Resetting Web Base Path${plain}"

    read -rp "Are you sure you want to reset the web base path? (y/n): " confirm
    if [[ $confirm != "y" && $confirm != "Y" ]]; then
        echo -e "${yellow}Operation canceled.${plain}"
        return
    fi

    config_webBasePath=$(gen_random_string 18)

    # Apply the new web base path setting
    ${xui_folder}/x-ui setting -webBasePath "${config_webBasePath}" > /dev/null 2>&1

    echo -e "Web base path has been reset to: ${green}${config_webBasePath}${plain}"
    echo -e "${green}Please use the new web base path to access the panel.${plain}"
    restart
}

reset_config() {
    confirm "Are you sure you want to reset all panel settings, Account data will not be lost, Username and password will not change" "n"
    if [[ $? != 0 ]]; then
        if [[ $# == 0 ]]; then
            show_menu
        fi
        return 0
    fi
    ${xui_folder}/x-ui setting -reset
    echo -e "All panel settings have been reset to default."
    restart
}

check_config() {
    local info=$(${xui_folder}/x-ui setting -show true)
    if [[ $? != 0 ]]; then
        LOGE "get current settings error, please check logs"
        show_menu
        return
    fi
    LOGI "${info}"

    local db_type="${XUI_DB_TYPE:-}"
    if [[ "$db_type" == "postgres" || "$db_type" == "postgresql" || "$db_type" == "pg" ]]; then
        if [[ -n "${XUI_DB_DSN:-}" ]]; then
            echo -e "${green}Database: External PostgreSQL (configured through RouterOS container environment)${plain}"
        else
            echo -e "${green}Database: External PostgreSQL (XUI_DB_DSN is not configured)${plain}"
        fi
    else
        echo -e "${green}Database: SQLite (/etc/x-ui/x-ui.db)${plain}"
    fi

    local existing_webBasePath=$(echo "$info" | grep -Eo 'webBasePath: .+' | awk '{print $2}')
    local existing_port=$(echo "$info" | grep -Eo 'port: .+' | awk '{print $2}')
    local existing_cert=$(${xui_folder}/x-ui setting -getCert true | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
    local URL_lists=(
        "https://api4.ipify.org"
        "https://ipv4.icanhazip.com"
        "https://v4.api.ipinfo.io/ip"
        "https://ipv4.myexternalip.com/raw"
        "https://4.ident.me"
        "https://check-host.net/ip"
    )
    local server_ip=""
    for ip_address in "${URL_lists[@]}"; do
        local response=$(curl -s -w "\n%{http_code}" --max-time 3 "${ip_address}" 2> /dev/null)
        local http_code=$(echo "$response" | tail -n1)
        local ip_result=$(echo "$response" | head -n-1 | tr -d '[:space:]"')
        if [[ "${http_code}" == "200" && "${ip_result}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            server_ip="${ip_result}"
            break
        fi
    done

    if [[ -z "$server_ip" ]]; then
        echo -e "${yellow}Could not auto-detect server IP from any provider.${plain}"
        while [[ -z "$server_ip" ]]; do
            read -rp "Please enter your server's public IPv4 address: " server_ip
            server_ip="${server_ip// /}"
            if [[ ! "$server_ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
                echo -e "${red}Invalid IPv4 address. Please try again.${plain}"
                server_ip=""
            fi
        done
    fi

    if [[ -n "$existing_cert" ]]; then
        local domain=$(basename "$(dirname "$existing_cert")")
        # The cert folder name is only the certificate's first domain. A
        # multidomain (SAN) certificate may be served under any name it covers,
        # so read the real names from the certificate itself (#5070).
        local cert_sans=""
        if [[ -f "$existing_cert" ]] && command -v openssl > /dev/null 2>&1; then
            cert_sans=$(openssl x509 -in "$existing_cert" -noout -ext subjectAltName 2> /dev/null \
                | grep -Eo 'DNS:[^,[:space:]]+' | cut -d: -f2)
            if [[ -n "$cert_sans" ]] && ! echo "$cert_sans" | grep -qx "$domain"; then
                domain=$(echo "$cert_sans" | head -n1)
            fi
        fi

        if [[ "$domain" =~ ^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${green}Access URL: https://${domain}:${existing_port}${existing_webBasePath}${plain}"
        else
            echo -e "${green}Access URL: https://${server_ip}:${existing_port}${existing_webBasePath}${plain}"
        fi
        if [[ -n "$cert_sans" && $(echo "$cert_sans" | wc -l) -gt 1 ]]; then
            echo -e "${yellow}The certificate also covers:${plain} $(echo "$cert_sans" | grep -vx "$domain" | tr '\n' ' ')"
        fi
    else
        echo -e "${red}⚠ WARNING: No TLS certificate configured for the panel!${plain}"
        echo -e "${yellow}Place a certificate and key under /root/cert, then select option 14 (TLS Certificates).${plain}"
        echo -e "${yellow}Access URL: http://${server_ip}:${existing_port}${existing_webBasePath}${plain}"
    fi
}

set_port() {
    echo -n "Enter port number[1-65535]: "
    read -r port
    if [[ -z "${port}" ]]; then
        LOGD "Cancelled"
        before_show_menu
    else
        ${xui_folder}/x-ui setting -port ${port}
        echo -e "The port is set, Please restart the panel now, and use the new port ${green}${port}${plain} to access web panel"
        confirm_restart
    fi
}

start() {
    check_status
    local status_code=$?
    if [[ "${running_in_docker}" == "true" ]]; then
        echo ""
        if [[ ${status_code} == 0 ]]; then
            LOGI "Panel is currently running inside the RouterOS container."
            LOGI "A stopped container cannot be started from inside itself."
            LOGI "To reload the panel web service and restart Xray internally, run: x-ui restart"
        else
            LOGE "Panel process is not running inside this container."
            LOGI "A stopped container cannot be started from inside itself."
        fi
        LOGI "To start the container from RouterOS Terminal or WinBox, run:"
        LOGI '  /container/start [find where comment="[Mikrozia:container] Container"]'
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    if [[ ${status_code} == 0 ]]; then
        echo ""
        LOGI "Panel is running, No need to start again, If you need to restart, please select restart"
    else
        if [[ $release == "alpine" ]]; then
            rc-service x-ui start
        else
            systemctl start x-ui
        fi
        sleep 2
        check_status
        if [[ $? == 0 ]]; then
            LOGI "x-ui Started Successfully"
        else
            LOGE "panel Failed to start, Probably because it takes longer than two seconds to start, Please check the log information later"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

stop() {
    check_status
    local status_code=$?
    if [[ "${running_in_docker}" == "true" ]]; then
        echo ""
        if [[ ${status_code} == 1 ]]; then
            LOGI "Panel process is not running inside this container."
        else
            LOGI "In RouterOS Container, the container lifecycle is managed by RouterOS."
        fi
        LOGI "Warning: stopping the container terminates all active Panel, Xray, MTProto, and internal connections."
        LOGI "This command does not stop PID 1 from inside the container. To stop it from RouterOS Terminal or WinBox, run:"
        LOGI '  /container/stop [find where comment="[Mikrozia:container] Container"]'
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    if [[ ${status_code} == 1 ]]; then
        echo ""
        LOGI "Panel stopped, No need to stop again!"
    else
        if [[ $release == "alpine" ]]; then
            rc-service x-ui stop
        else
            systemctl stop x-ui
        fi
        sleep 2
        check_status
        if [[ $? == 1 ]]; then
            LOGI "x-ui and xray stopped successfully"
        else
            LOGE "Panel stop failed, Probably because the stop time exceeds two seconds, Please check the log information later"
        fi
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart() {
    if [[ "${running_in_docker}" == "true" ]]; then
        local rc=0
        if signal_xui HUP; then
            sleep 1
            if signal_xui USR1; then
                sleep 2
                check_status
                if [[ $? == 0 ]]; then
                    LOGI "Panel web service reloaded and Xray restart signal sent."
                else
                    LOGE "Panel is not running after reload signal; check container logs."
                    rc=1
                fi
            else
                LOGE "Could not send Xray restart signal (SIGUSR1) to the panel process."
                rc=1
            fi
        else
            LOGE "Could not find the running panel process to signal."
            rc=1
        fi
        LOGI "The RouterOS container itself was not restarted."
        LOGI 'For a full container restart from RouterOS Terminal or WinBox, run: /container/restart [find where comment="[Mikrozia:container] Container"]'
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return "${rc}"
    fi
    if [[ $release == "alpine" ]]; then
        rc-service x-ui restart
    else
        systemctl restart x-ui
    fi
    sleep 2
    check_status
    if [[ $? == 0 ]]; then
        LOGI "x-ui and xray Restarted successfully"
    else
        LOGE "Panel restart failed, Probably because it takes longer than two seconds to start, Please check the log information later"
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

restart_xray() {
    if [[ "${running_in_docker}" == "true" ]]; then
        local rc=0
        if signal_xui USR1; then
            LOGI "Xray restart signal (SIGUSR1) sent to the panel process."
            sleep 2
            show_xray_status
        else
            LOGE "Could not find the running panel process to signal Xray restart."
            rc=1
        fi
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return "${rc}"
    fi
    if [[ $release == "alpine" ]]; then
        rc-service x-ui reload
    else
        systemctl reload x-ui
    fi
    LOGI "xray-core Restart signal sent successfully, Please check the log information to confirm whether xray restarted successfully"
    sleep 2
    show_xray_status
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

status() {
    if [[ "${running_in_docker}" == "true" ]]; then
        show_status
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    if [[ $release == "alpine" ]]; then
        rc-service x-ui status
    else
        systemctl status x-ui -l
    fi
    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

enable() {
    if [[ "${running_in_docker}" == "true" ]]; then
        LOGI "Container autostart is managed by RouterOS, not inside the container."
        LOGI "To enable autostart on boot and automatic restart in RouterOS Terminal or WinBox (RouterOS 7.23+, including 7.24.6 Long-term and 7.25.1 Stable), run:"
        LOGI '  /container/set [find where comment="[Mikrozia:container] Container"] start-on-boot=yes restart-policy=always'
        LOGI "Note: start-on-boot controls startup on RouterOS boot; restart-policy requires RouterOS 7.23 or newer."
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    if [[ $release == "alpine" ]]; then
        rc-update add x-ui default
    else
        systemctl enable x-ui
    fi
    if [[ $? == 0 ]]; then
        LOGI "x-ui Set to boot automatically on startup successfully"
    else
        LOGE "x-ui Failed to set Autostart"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

disable() {
    if [[ "${running_in_docker}" == "true" ]]; then
        LOGI "Container autostart is managed by RouterOS, not inside the container."
        LOGI "To disable autostart on boot and automatic restart in RouterOS Terminal or WinBox (RouterOS 7.23+, including 7.24.6 Long-term and 7.25.1 Stable), run:"
        LOGI '  /container/set [find where comment="[Mikrozia:container] Container"] start-on-boot=no restart-policy=no'
        LOGI "Note: start-on-boot controls startup on RouterOS boot; restart-policy requires RouterOS 7.23 or newer."
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 0
    fi
    if [[ $release == "alpine" ]]; then
        rc-update del x-ui
    else
        systemctl disable x-ui
    fi
    if [[ $? == 0 ]]; then
        LOGI "x-ui Autostart Cancelled successfully"
    else
        LOGE "x-ui Failed to cancel autostart"
    fi

    if [[ $# == 0 ]]; then
        before_show_menu
    fi
}

show_log() {
    if [[ $release == "alpine" ]]; then
        echo -e "${green}\t1.${plain} Debug Log"
        echo -e "${green}\t0.${plain} Back to Main Menu"
        read -rp "Choose an option: " choice

        case "$choice" in
            0)
                show_menu
                ;;
            1)
                local app_log="${log_folder}/3xui.log"
                if [[ -f "${app_log}" ]]; then
                    ls -lh "${app_log}"
                    tail -n 50 "${app_log}"
                else
                    LOGI "No active file log at ${app_log}; in RouterOS Container, panel logs also stream to stdout/stderr."
                fi
                LOGI "RouterOS Container logs (Terminal/WinBox):"
                LOGI '  /container/set [find where comment="[Mikrozia:container] Container"] logging=yes'
                LOGI '  /log/print follow-only where topics~"container"'
                if [[ $# == 0 ]]; then
                    before_show_menu
                fi
                ;;
            *)
                echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
                show_log
                ;;
        esac
    else
        echo -e "${green}\t1.${plain} Debug Log"
        echo -e "${green}\t2.${plain} Clear All logs"
        echo -e "${green}\t0.${plain} Back to Main Menu"
        read -rp "Choose an option: " choice

        case "$choice" in
            0)
                show_menu
                ;;
            1)
                journalctl -u x-ui -e --no-pager -f -p debug
                if [[ $# == 0 ]]; then
                    before_show_menu
                fi
                ;;
            2)
                sudo journalctl --rotate
                sudo journalctl --vacuum-time=1s
                echo "All Logs cleared."
                restart
                ;;
            *)
                echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
                show_log
                ;;
        esac
    fi
}

xui_pid() {
    ps -ef 2> /dev/null | grep -F "${xui_folder}/x-ui" | grep -v -E "grep|/usr/bin/x-ui|sh -c" | awk -v self="$$" '$1 != self {print $1; exit}'
}

signal_xui() {
    local sig="$1" pid
    pid="$(xui_pid)"
    if [[ -z "${pid}" ]]; then
        return 1
    fi
    kill -"${sig}" "${pid}" 2> /dev/null
}

# 0: running, 1: not running, 2: not installed
check_status() {
    if [[ "${running_in_docker}" == "true" ]]; then
        if [[ ! -x "${xui_folder}/x-ui" ]]; then
            return 2
        fi
        if [[ -n "$(xui_pid)" ]]; then
            return 0
        else
            return 1
        fi
    fi
    if [[ $release == "alpine" ]]; then
        if [[ ! -f /etc/init.d/x-ui ]]; then
            return 2
        fi
        if [[ $(rc-service x-ui status | grep -F 'status: started' -c) == 1 ]]; then
            return 0
        else
            return 1
        fi
    else
        if [[ ! -f ${xui_service}/x-ui.service ]]; then
            return 2
        fi
        temp=$(systemctl show --property=SubState x-ui)
        temp=${temp#SubState=}
        if [[ "${temp}" == "running" ]]; then
            return 0
        else
            return 1
        fi
    fi
}

check_enabled() {
    if [[ $release == "alpine" ]]; then
        if [[ $(rc-update show | grep -F 'x-ui' | grep default -c) == 1 ]]; then
            return 0
        else
            return 1
        fi
    else
        temp=$(systemctl is-enabled x-ui)
        if [[ "${temp}" == "enabled" ]]; then
            return 0
        else
            return 1
        fi
    fi
}

check_install() {
    check_status
    if [[ $? == 2 ]]; then
        echo ""
        LOGE "Please install the panel first"
        if [[ $# == 0 ]]; then
            before_show_menu
        fi
        return 1
    else
        return 0
    fi
}

show_status() {
    check_status
    case $? in
        0)
            echo -e "Panel state: ${green}Running${plain}"
            show_enable_status
            ;;
        1)
            echo -e "Panel state: ${yellow}Not Running${plain}"
            show_enable_status
            ;;
        2)
            echo -e "Panel state: ${red}Not Installed${plain}"
            ;;
    esac
    show_xray_status
    show_mtproto_status
}

show_enable_status() {
    if [[ "${running_in_docker}" == "true" ]]; then
        echo -e "Start automatically: ${green}Managed by RouterOS (check /container/print detail)${plain}"
        echo -e "  ${blue}/container/print detail where comment=\"[Mikrozia:container] Container\"${plain}"
        return
    fi
    check_enabled
    if [[ $? == 0 ]]; then
        echo -e "Start automatically: ${green}Yes${plain}"
    else
        echo -e "Start automatically: ${red}No${plain}"
    fi
}

check_xray_status() {
    count=$(ps -ef | grep "xray-linux" | grep -v "grep" | wc -l)
    if [[ count -ne 0 ]]; then
        return 0
    else
        return 1
    fi
}

show_xray_status() {
    check_xray_status
    if [[ $? == 0 ]]; then
        echo -e "xray state: ${green}Running${plain}"
    else
        echo -e "xray state: ${red}Not Running${plain}"
    fi
}

# show_mtproto_status reports each mtproto inbound's mtg sidecar (one process per
# inbound, run outside xray). Silent when no mtproto inbound is configured.
show_mtproto_status() {
    local cfg_dir="${xui_folder}/bin/mtproto"
    local cfgs=()
    if [[ -d "${cfg_dir}" ]]; then
        for f in "${cfg_dir}"/mtg-*.toml; do
            [[ -e "$f" ]] && cfgs+=("$f")
        done
    fi
    [[ ${#cfgs[@]} -eq 0 ]] && return

    local running
    running=$(ps -ef | grep "mtg-linux" | grep -v "grep" | grep -oE 'mtg-[0-9]+\.toml')
    for f in "${cfgs[@]}"; do
        local name id bind
        name=$(basename "$f")
        id=$(echo "${name}" | sed -E 's/mtg-([0-9]+)\.toml/\1/')
        bind=$(grep -E '^[[:space:]]*bind-to' "$f" | head -1 | cut -d'"' -f2)
        if echo "${running}" | grep -qx "${name}"; then
            echo -e "mtproto inbound ${id} (${bind}): ${green}Running${plain}"
        else
            echo -e "mtproto inbound ${id} (${bind}): ${red}Not Running${plain}"
        fi
    done
}

tls_cert_path_menu() {
    echo -e "${yellow}Certificates are issued by the ACME client bundled in the image and stored on /root/cert.${plain}"
    echo -e "${green}\t1.${plain} Show certificate status (panel, subscription, expiry, renewal mode)"
    echo -e "${green}\t2.${plain} Set certificate paths for the panel (manual import)"
    echo -e "${green}\t3.${plain} Issue or renew a certificate with HTTP-01 (manual)"
    echo -e "${green}\t4.${plain} Cloudflare DNS-01 setup and automatic renewal"
    echo -e "${green}\t5.${plain} Show existing certificates under /root/cert"
    echo -e "${green}\t0.${plain} Back to Main Menu"

    read -rp "Choose an option: " choice
    case "$choice" in
        0)
            show_menu
            ;;
        1)
            "${xui_folder}/x-ui" acme status
            echo ""
            echo -e "${blue}Panel and subscription certificate paths:${plain}"
            "${xui_folder}/x-ui" setting -getCert true
            tls_cert_path_menu
            ;;
        2)
            echo -e "${green}\t1.${plain} Use a certificate from /root/cert"
            echo -e "${green}\t2.${plain} Enter custom certificate file paths"
            read -rp "Choose an option: " pathChoice
            if [[ "$pathChoice" == "2" ]]; then
                read -rp "Certificate file path (fullchain): " webCertFile
                read -rp "Private key file path: " webKeyFile
                if [[ -f "${webCertFile}" && -f "${webKeyFile}" ]]; then
                    "${xui_folder}/x-ui" cert -webCert "$webCertFile" -webCertKey "$webKeyFile"
                    echo "Panel certificate paths set:"
                    echo "  - Certificate File: $webCertFile"
                    echo "  - Private Key File: $webKeyFile"
                    restart
                    return
                else
                    echo "Certificate or private key file not found."
                fi
                tls_cert_path_menu
                return
            fi
            local domains
            domains=$(find /root/cert/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2> /dev/null)
            if [ -z "$domains" ]; then
                echo "No certificates found."
            else
                echo "Available domains:"
                echo "$domains"
                read -rp "Please choose a domain to set the panel paths: " domain

                if echo "$domains" | grep -qw "$domain"; then
                    local webCertFile="/root/cert/${domain}/fullchain.pem"
                    local webKeyFile="/root/cert/${domain}/privkey.pem"

                    if [[ -f "${webCertFile}" && -f "${webKeyFile}" ]]; then
                        "${xui_folder}/x-ui" cert -webCert "$webCertFile" -webCertKey "$webKeyFile"
                        echo "Panel paths set for domain: $domain"
                        echo "  - Certificate File: $webCertFile"
                        echo "  - Private Key File: $webKeyFile"
                        restart
                        return
                    else
                        echo "Certificate or private key not found for domain: $domain."
                    fi
                else
                    echo "Invalid domain entered."
                fi
            fi
            tls_cert_path_menu
            ;;
        3)
            read -rp "Domain for the certificate (for example panel.example.com): " acme_domain
            if [[ -z "${acme_domain}" ]]; then
                echo "No domain entered."
                tls_cert_path_menu
                return
            fi
            echo -e "${yellow}Mikrozia cannot verify public HTTP-01 reachability from inside the container.${plain}"
            echo -e "${yellow}The RouterOS operator must temporarily make TCP 80 reachable from the public Internet.${plain}"
            echo -e "${yellow}Mikrozia never inspects or changes RouterOS firewall, NAT, or port-forward rules.${plain}"
            "${xui_folder}/x-ui" acme check --http --domain "${acme_domain}"
            read -rp "Start the issuance now? (y/n): " acme_confirm
            if [[ "$acme_confirm" == "y" || "$acme_confirm" == "Y" ]]; then
                "${xui_folder}/x-ui" acme issue --http --domain "${acme_domain}"
                return
            fi
            tls_cert_path_menu
            ;;
        4)
            read -rp "Domain for the certificate (for example panel.example.com): " acme_domain
            if [[ -z "${acme_domain}" ]]; then
                echo "No domain entered."
                tls_cert_path_menu
                return
            fi
            echo -e "${yellow}Use a Cloudflare API Token with Zone:Read and DNS:Edit for this zone only.${plain}"
            echo -e "${yellow}The token is never printed, logged, or returned by the panel API.${plain}"
            echo -e "${yellow}RouterOS Environment values are visible to anyone who can read or export RouterOS configuration.${plain}"
            read -rs -p "Paste the Cloudflare API token (the input stays hidden): " acme_token
            echo ""
            if [[ -z "${acme_token}" ]]; then
                echo "No token entered."
                tls_cert_path_menu
                return
            fi
            printf '%s' "${acme_token}" | "${xui_folder}/x-ui" acme cloudflare setup --domain "${acme_domain}" --stdin
            unset acme_token
            "${xui_folder}/x-ui" acme check --dns --domain "${acme_domain}"
            read -rp "Start the issuance now? (y/n): " acme_confirm
            if [[ "$acme_confirm" == "y" || "$acme_confirm" == "Y" ]]; then
                "${xui_folder}/x-ui" acme issue --dns --domain "${acme_domain}"
                return
            fi
            tls_cert_path_menu
            ;;
        5)
            local domains
            domains=$(find /root/cert/ -mindepth 1 -maxdepth 1 -type d -exec basename {} \; 2> /dev/null)
            if [ -z "$domains" ]; then
                echo "No certificates found under /root/cert."
            else
                echo "Existing domains and their paths:"
                for domain in $domains; do
                    local cert_path="/root/cert/${domain}/fullchain.pem"
                    local key_path="/root/cert/${domain}/privkey.pem"
                    if [[ -f "${cert_path}" && -f "${key_path}" ]]; then
                        echo -e "Domain: ${domain}"
                        echo -e "\tCertificate Path: ${cert_path}"
                        echo -e "\tPrivate Key Path: ${key_path}"
                    else
                        echo -e "Domain: ${domain} - Certificate or Key missing."
                    fi
                done
            fi
            # The panel's configured certificate may live outside /root/cert
            # (e.g. a certificate mounted from RouterOS) — show it too (#5070).
            local panel_cert
            panel_cert=$("${xui_folder}/x-ui" setting -getCert true | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
            if [[ -n "${panel_cert}" && "${panel_cert}" != /root/cert/* ]]; then
                echo -e "Panel certificate (custom path): ${panel_cert}"
                if [[ -f "${panel_cert}" ]] && command -v openssl > /dev/null 2>&1; then
                    local panel_sans
                    panel_sans=$(openssl x509 -in "${panel_cert}" -noout -ext subjectAltName 2> /dev/null \
                        | grep -Eo 'DNS:[^,[:space:]]+' | cut -d: -f2 | tr '\n' ' ')
                    [[ -n "${panel_sans}" ]] && echo -e "\tCovers: ${panel_sans}"
                fi
            fi
            tls_cert_path_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            tls_cert_path_menu
            ;;
    esac
}

# Database tools (SQLite .db <-> .dump conversion & external PostgreSQL guidance).
external_postgres_guide() {
    echo ""
    echo -e "${blue}External PostgreSQL Setup (RouterOS Container - Advanced)${plain}"
    echo -e "  - Default backend: ${green}SQLite${plain} (/etc/x-ui/x-ui.db) — recommended for most setups."
    echo -e "  - External PostgreSQL (supported versions: 15–18) is configured via RouterOS ${green}/container/envs${plain}:"
    echo -e "      ${green}XUI_DB_TYPE=postgres${plain}"
    echo -e "      ${green}XUI_DB_DSN=postgres://user:password@host:5432/dbname?sslmode=require${plain}"
    echo -e "  - To switch backend, recreate/replace the container with the updated envlist and the same persistent mounts (/etc/x-ui and /root/cert)."
    echo -e "  - Mikrozia never creates or changes RouterOS firewall, NAT, or routing rules."
    echo -e "  - Reference: ${blue}docs/reference/database${plain}"
    echo ""
}

database_tools_menu() {
    echo -e "${green}\t1.${plain} Convert SQLite ${green}.db <-> .dump${plain}"
    echo -e "${green}\t2.${plain} External PostgreSQL Guide (RouterOS /container/envs)"
    echo -e "${green}\t0.${plain} Back to Main Menu"
    read -rp "Choose an option: " choice
    case "$choice" in
        0)
            show_menu
            ;;
        1)
            migrate_db_prompt
            database_tools_menu
            ;;
        2)
            external_postgres_guide
            database_tools_menu
            ;;
        *)
            echo -e "${red}Invalid option. Please select a valid number.${plain}\n"
            database_tools_menu
            ;;
    esac
}

# Convert between the panel's SQLite database and a portable .dump (SQL text)
# file using the bundled x-ui binary. With no arguments it dumps the installed
# panel database; an optional second argument overrides the output path.
#   x-ui migrateDB [file.db|file.dump] [output]
migrate_db() {
    local input="$1" output="$2"
    local default_db="${XUI_DB_FOLDER:-/etc/x-ui}/x-ui.db"
    local bin="${xui_folder}/x-ui"

    [[ -z "$input" ]] && input="$default_db"

    if [[ ! -x "$bin" ]]; then
        LOGE "x-ui binary not found at ${bin}. Is the panel installed?"
        return 1
    fi

    if ! "$bin" migrate-db -h 2>&1 | grep -q -- '-dump'; then
        LOGE "This x-ui build does not support .db <-> .dump conversion yet."
        LOGE "Update the Mikrozia container image to a version with 'migrate-db --dump/--restore'."
        return 1
    fi

    if [[ ! -f "$input" ]]; then
        LOGE "Input file not found: ${input}"
        echo -e "Usage: ${green}x-ui migrateDB [file.db|file.dump] [output]${plain}"
        return 1
    fi

    local mode
    case "$input" in
        *.db | *.sqlite | *.sqlite3)
            mode="dump"
            ;;
        *.dump | *.sql)
            mode="restore"
            ;;
        *)
            if head -c 16 "$input" | grep -q "SQLite format 3"; then
                mode="dump"
            else
                mode="restore"
            fi
            ;;
    esac

    local tmp_dir="" tmp_output=""
    cleanup_migrate_tmp() {
        if [[ -n "$tmp_dir" ]]; then
            rm -rf -- "$tmp_dir"
        fi
    }

    if [[ "$mode" == "dump" ]]; then
        [[ -z "$output" ]] && output="${input%.*}.dump"
        if [[ -f "$output" ]]; then
            confirm "Output ${output} already exists and will be overwritten. Continue?" "n" || return 0
        fi
        if ! tmp_dir=$(mktemp -d "${output}.mikrozia-tmp.XXXXXX"); then
            LOGE "Cannot create a temporary directory next to ${output}."
            return 1
        fi
        tmp_output="${tmp_dir}/output"
        LOGI "Dumping SQLite database to SQL text:"
        echo -e "  ${green}${input}${plain} -> ${green}${output}${plain}"
        if ! "$bin" migrate-db --src "$input" --dump "$tmp_output"; then
            LOGE "Dump failed. ${output} was not changed."
            cleanup_migrate_tmp
            return 1
        fi
        if ! mv -f -- "$tmp_output" "$output"; then
            LOGE "Dump completed but ${output} could not be replaced."
            cleanup_migrate_tmp
            return 1
        fi
        cleanup_migrate_tmp
        LOGI "Done. Wrote ${output}."
    else
        [[ -z "$output" ]] && output="${input%.*}.db"
        if [[ "$output" == "$default_db" ]] && check_status > /dev/null 2>&1; then
            LOGE "Refusing to restore into the live database (${default_db}) while x-ui is running."
            LOGE "Stop the panel first (x-ui stop) or choose a different output path."
            return 1
        fi
        if [[ -f "$output" ]]; then
            confirm "Output ${output} already exists and will be overwritten. Continue?" "n" || return 0
        fi
        if ! tmp_dir=$(mktemp -d "${output}.mikrozia-tmp.XXXXXX"); then
            LOGE "Cannot create a temporary directory next to ${output}."
            return 1
        fi
        tmp_output="${tmp_dir}/output"
        LOGI "Rebuilding SQLite database from SQL text:"
        echo -e "  ${green}${input}${plain} -> ${green}${output}${plain}"
        if ! "$bin" migrate-db --restore "$input" --out "$tmp_output"; then
            LOGE "Restore failed. ${output} was not changed."
            cleanup_migrate_tmp
            return 1
        fi
        if ! mv -f -- "$tmp_output" "$output"; then
            LOGE "Restore completed but ${output} could not be replaced."
            cleanup_migrate_tmp
            return 1
        fi
        # A stale WAL/SHM/journal from the previous database must never be
        # replayed against the restored database.
        local sidecar
        for sidecar in "${output}-wal" "${output}-shm" "${output}-journal"; do
            if [[ -e "$sidecar" ]]; then
                if ! rm -f -- "$sidecar"; then
                    LOGE "Restore completed but stale sidecar ${sidecar} could not be removed."
                    cleanup_migrate_tmp
                    return 1
                fi
            fi
        done
        cleanup_migrate_tmp
        LOGI "Done. Created ${output}."
    fi
}

# Interactive wrapper around migrate_db for the menu: prompts for the paths and
# lets migrate_db auto-detect the direction.
migrate_db_prompt() {
    local default_db="${XUI_DB_FOLDER:-/etc/x-ui}/x-ui.db"
    local input output
    echo -e "Convert between a SQLite ${green}.db${plain} and a portable ${green}.dump${plain} (direction auto-detected)."
    read -rp "Input file [${default_db}]: " input
    input="${input:-$default_db}"
    read -rp "Output file (leave empty to auto-name next to input): " output
    migrate_db "$input" "$output"
}

network_policy_notice() {
    echo -e "${yellow}Mikrozia does not create or manage firewall, NAT, port-forwarding, or IP-limit rules.${plain}"
    echo -e "${yellow}Configure all network access policies directly in RouterOS.${plain}"
    before_show_menu
}

show_usage() {
    echo -e "┌────────────────────────────────────────────────────────────────┐
│  ${blue}x-ui control menu usages (subcommands):${plain}                       │
│                                                                │
│  ${blue}x-ui${plain}                       - Admin Management Script          │
│  ${blue}x-ui version${plain}               - Show Mikrozia version            │
│  ${blue}x-ui start${plain}                 - Show RouterOS Start Command      │
│  ${blue}x-ui stop${plain}                  - Show RouterOS Stop Command       │
│  ${blue}x-ui restart${plain}               - Reload Panel & Restart Xray      │
│  ${blue}x-ui restart-xray${plain}          - Restart Xray Only                │
│  ${blue}x-ui status${plain}                - Current Status                   │
│  ${blue}x-ui settings${plain}              - Current Settings                 │
│  ${blue}x-ui setting [flags]${plain}        - Direct CLI setting configuration │
│  ${blue}x-ui enable${plain}                - Show RouterOS Enable Autostart   │
│  ${blue}x-ui disable${plain}               - Show RouterOS Disable Autostart  │
│  ${blue}x-ui log${plain}                   - Check logs                       │
│  ${blue}x-ui cert [flags]${plain}          - Configure certificate file paths  │
│  ${blue}x-ui acme <command>${plain}          - Issue, renew, inspect certs      │
│  ${blue}x-ui migrateDB [file]${plain}      - Convert .db <-> .dump (SQLite)   │
└────────────────────────────────────────────────────────────────┘"
}

show_menu() {
    echo -e "
╔────────────────────────────────────────────────╗
│  ${green}Mikrozia Panel Management Script${plain}             │
│  ${green}0.${plain} Exit Script                               │
│────────────────────────────────────────────────│
│  ${green}1.${plain} Environment-Managed Admin Credentials          │
│  ${green}2.${plain} Reset Web Base Path                       │
│  ${green}3.${plain} Reset Settings                            │
│  ${green}4.${plain} Change Port                              │
│  ${green}5.${plain} View Current Settings                    │
│────────────────────────────────────────────────│
│  ${green}6.${plain} Show RouterOS Start Command                │
│  ${green}7.${plain} Show RouterOS Stop Command                 │
│  ${green}8.${plain} Reload Panel & Restart Xray                │
│  ${green}9.${plain} Restart Xray Only                          │
│  ${green}10.${plain} Check Status                             │
│  ${green}11.${plain} Logs Management                          │
│────────────────────────────────────────────────│
│  ${green}12.${plain} Show RouterOS Enable Autostart Command    │
│  ${green}13.${plain} Show RouterOS Disable Autostart Command   │
│────────────────────────────────────────────────│
│  ${green}14.${plain} TLS Certificates                              │
│  ${green}15.${plain} RouterOS Network Policy Notice            │
│  ${green}16.${plain} Database Tools                            │
╚────────────────────────────────────────────────╝
"
    show_status
    echo && read -rp "Please enter your selection [0-16]: " num

    case "${num}" in
        0)
            exit 0
            ;;
        1)
            check_install && admin_credential_menu
            ;;
        2)
            check_install && reset_webbasepath
            ;;
        3)
            check_install && reset_config
            ;;
        4)
            check_install && set_port
            ;;
        5)
            check_install && check_config
            ;;
        6)
            check_install && start
            ;;
        7)
            check_install && stop
            ;;
        8)
            check_install && restart
            ;;
        9)
            check_install && restart_xray
            ;;
        10)
            check_install && status
            ;;
        11)
            check_install && show_log
            ;;
        12)
            check_install && enable
            ;;
        13)
            check_install && disable
            ;;
        14)
            tls_cert_path_menu
            ;;
        15)
            network_policy_notice
            ;;
        16)
            database_tools_menu
            ;;
        *)
            LOGE "Please enter the correct number [0-16]"
            ;;
    esac
}

if [[ $# -gt 0 ]]; then
    case $1 in
        "version" | "-v" | "--version")
            exec "${xui_folder}/x-ui" "$@"
            ;;
        "start")
            check_install 0 && start 0
            ;;
        "stop")
            check_install 0 && stop 0
            ;;
        "restart")
            check_install 0 && restart 0
            ;;
        "restart-xray")
            check_install 0 && restart_xray 0
            ;;
        "status")
            check_install 0 && status 0
            ;;
        "settings")
            check_install 0 && check_config 0
            ;;
        "setting" | "cert" | "acme")
            exec "${xui_folder}/x-ui" "$@"
            ;;
        "enable")
            check_install 0 && enable 0
            ;;
        "disable")
            check_install 0 && disable 0
            ;;
        "log")
            check_install 0 && show_log 0
            ;;
        "migrateDB")
            migrate_db "$2" "$3"
            ;;
        "migrate-db")
            exec "${xui_folder}/x-ui" "$@"
            ;;
        *) show_usage ;;
    esac
else
    show_menu
fi
