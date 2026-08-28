package unifi

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// FlexInt tolerates UniFi's habit of returning numbers as either JSON numbers
// or strings depending on firmware version.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	b = bytes.Trim(bytes.TrimSpace(b), `"`)
	s := string(b)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = FlexInt(int(n))
	return nil
}

func (f FlexInt) Int() int { return int(f) }

var _ json.Unmarshaler = (*FlexInt)(nil)

// FirewallRule is a classic (pre zone-based) firewall rule. Only the fields
// rampart displays are typed; updates always round-trip the raw object.
type FirewallRule struct {
	ID         string  `json:"_id"`
	Name       string  `json:"name"`
	Ruleset    string  `json:"ruleset"`
	RuleIndex  FlexInt `json:"rule_index"`
	Action     string  `json:"action"`
	Enabled    bool    `json:"enabled"`
	Protocol   string  `json:"protocol"`
	SrcAddress string  `json:"src_address"`
	SrcPort    string  `json:"src_port"`
	DstAddress string  `json:"dst_address"`
	DstPort    string  `json:"dst_port"`
	Logging    bool    `json:"logging"`
}

// FirewallPolicy is a zone-based firewall policy (UniFi Network 9+).
type FirewallPolicy struct {
	ID          string         `json:"_id"`
	Name        string         `json:"name"`
	Action      string         `json:"action"`
	Enabled     bool           `json:"enabled"`
	Index       FlexInt        `json:"index"`
	Predefined  bool           `json:"predefined"`
	Protocol    string         `json:"protocol"`
	Source      PolicyEndpoint `json:"source"`
	Destination PolicyEndpoint `json:"destination"`
}

type PolicyEndpoint struct {
	ZoneID string `json:"zone_id"`
}

// PortForward is a port-forwarding rule (classic /rest/portforward endpoint).
// Only displayed fields are typed; updates always round-trip the raw object.
type PortForward struct {
	ID        string `json:"_id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Interface string `json:"pfwd_interface"` // wan, wan2
	Fwd       string `json:"fwd"`            // forward-to (internal) address
	FwdPort   string `json:"fwd_port"`       // internal port
	DstPort   string `json:"dst_port"`       // external (WAN) port
	Src       string `json:"src"`            // source restriction: "any" or CIDR
	Proto     string `json:"proto"`          // tcp, udp, tcp_udp
	Log       bool   `json:"log"`
}

type FirewallZone struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
}

type FirewallGroup struct {
	ID      string   `json:"_id"`
	Name    string   `json:"name"`
	Type    string   `json:"group_type"`
	Members []string `json:"group_members"`
}

type Device struct {
	ID      string  `json:"_id"`
	Name    string  `json:"name"`
	Model   string  `json:"model"`
	Type    string  `json:"type"`
	MAC     string  `json:"mac"`
	IP      string  `json:"ip"`
	Version string  `json:"version"`
	State   FlexInt `json:"state"`
	Uptime  FlexInt `json:"uptime"`
}

func (d Device) StateString() string {
	switch d.State.Int() {
	case 0:
		return "offline"
	case 1:
		return "online"
	case 2:
		return "pending adoption"
	case 4:
		return "upgrading"
	case 5:
		return "provisioning"
	case 6:
		return "heartbeat missed"
	default:
		return strconv.Itoa(d.State.Int())
	}
}

// Sta is a connected client station (wired or wireless).
type Sta struct {
	ID       string  `json:"_id"`
	Name     string  `json:"name"`
	Hostname string  `json:"hostname"`
	OUI      string  `json:"oui"`
	MAC      string  `json:"mac"`
	IP       string  `json:"ip"`
	Network  string  `json:"network"`
	IsWired  bool    `json:"is_wired"`
	Uptime   FlexInt `json:"uptime"`
}

func (s Sta) DisplayName() string {
	switch {
	case s.Name != "":
		return s.Name
	case s.Hostname != "":
		return s.Hostname
	case s.OUI != "":
		return s.OUI
	default:
		return "-"
	}
}
