package privilegedagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"syscall"
	"time"

	networkinventory "github.com/ASSever/debian-router-b5-2840/backend/internal/network"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/proxy"
)

const (
	defaultSocketPath = "/run/debian-router-agent/agent.sock"
	maxRequestBytes   = 16384
	maxResponseBytes  = 16384
	connectionTimeout = 20 * time.Second
)

// Request is the only message accepted by the privileged agent.
type Request struct {
	ProxyProfile      *proxy.ProfileChange                `json:"proxy_profile,omitempty"`
	RoutingConfig     *networkinventory.RoutingConfig     `json:"routing_config,omitempty"`
	FirewallRules     *networkinventory.FirewallRules     `json:"firewall_rules,omitempty"`
	DNSConfig         *networkinventory.LANDNSConfig      `json:"dns_config,omitempty"`
	ReservationChange *networkinventory.ReservationChange `json:"reservation_change,omitempty"`
	LANConfig         *networkinventory.LANConfig         `json:"lan_config,omitempty"`
	LANDestination    string                              `json:"lan_destination,omitempty"`
	WANLineConfig     *networkinventory.WANLineConfig     `json:"wan_line_config,omitempty"`
	Interface         string                              `json:"interface,omitempty"`
	Operation         string                              `json:"operation"`
	StaticWANConfig   *networkinventory.StaticWANConfig   `json:"static_wan_config,omitempty"`
	PPPoEConfig       *networkinventory.PPPoEConfig       `json:"pppoe_config,omitempty"`
	TransactionID     string                              `json:"transaction_id,omitempty"`
}

// Response contains harmless status data or a stable error code.
type Response struct {
	Traffic              *networkinventory.TrafficSnapshot      `json:"traffic,omitempty"`
	ProxyProfile         *proxy.Profile                         `json:"proxy_profile,omitempty"`
	WANProbeState        *networkinventory.WANProbeState        `json:"wan_probe_state,omitempty"`
	RoutingStatus        *networkinventory.RoutingStatus        `json:"routing_status,omitempty"`
	RuleSettings         *networkinventory.FirewallRulesStatus  `json:"firewall_rules,omitempty"`
	Firewall             *networkinventory.FirewallStatus       `json:"firewall,omitempty"`
	DNSStatus            *networkinventory.LANDNSStatus         `json:"dns_status,omitempty"`
	Reservations         *networkinventory.DHCPReservations     `json:"reservations,omitempty"`
	LANStatus            *networkinventory.LANStatus            `json:"lan_status,omitempty"`
	LANConflict          *networkinventory.LANConflict          `json:"lan_conflict,omitempty"`
	WANLineStatus        *networkinventory.WANLineStatus        `json:"wan_line_status,omitempty"`
	DHCPStatus           *networkinventory.DHCPStatus           `json:"dhcp_status,omitempty"`
	StaticWANTransaction *networkinventory.StaticWANTransaction `json:"static_wan_transaction,omitempty"`
	PPPoETransaction     *networkinventory.PPPoETransaction     `json:"pppoe_transaction,omitempty"`
	PPPoEStatus          *networkinventory.PPPoEStatus          `json:"pppoe_status,omitempty"`
	Status               string                                 `json:"status,omitempty"`
	Agent                string                                 `json:"agent,omitempty"`
	Error                string                                 `json:"error,omitempty"`
}

var proxyProfiles = proxy.NewProfileManager("/var/lib/debian-router-agent/proxy-routing")

type staticWANOperations interface {
	Apply(context.Context, networkinventory.StaticWANConfig) (networkinventory.StaticWANTransaction, error)
	Confirm(context.Context, string) error
	Rollback(context.Context, string) error
}

type pppoeOperations interface {
	Apply(context.Context, networkinventory.PPPoEConfig) (networkinventory.PPPoETransaction, error)
	Confirm(context.Context, string) error
	Rollback(context.Context, string) error
	Reconnect(context.Context) error
	Status() (networkinventory.PPPoEStatus, error)
}

// DefaultSocketPath returns the systemd-managed agent socket path.
func DefaultSocketPath() string {
	return defaultSocketPath
}

// Serve accepts bounded requests from peers whose primary GID is allowedGID.
func Serve(ctx context.Context, socketPath string, allowedGID uint32) error {
	manager, err := networkinventory.NewStaticWANManager()
	if err != nil {
		return err
	}
	if err := manager.RecoverPending(ctx); err != nil {
		return fmt.Errorf("recover pending WAN configuration: %w", err)
	}
	if err := manager.RecoverLAN(ctx); err != nil {
		log.Printf("restore LAN services: %v", err)
	}
	pppoe, err := networkinventory.NewPPPoEManager()
	if err != nil {
		return err
	}
	if err := pppoe.RecoverPending(ctx); err != nil {
		return fmt.Errorf("recover pending PPPoE configuration: %w", err)
	}
	if err := proxyProfiles.Recover(ctx); err != nil {
		log.Printf("restore proxy profile: %v", err)
	}
	go manager.MaintainFirewall(ctx)
	trafficContext, stopTraffic := context.WithCancel(ctx)
	trafficDone := make(chan struct{})
	go func() {
		defer close(trafficDone)
		manager.MaintainTraffic(trafficContext)
	}()
	defer func() {
		stopTraffic()
		<-trafficDone
	}()
	return serveWithAllOperations(ctx, socketPath, allowedGID, networkinventory.ReadDHCPStatus, manager, pppoe)
}

func serveWithDHCPReader(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) error {
	return serveWithOperations(ctx, socketPath, allowedGID, readDHCPStatus, nil)
}

func serveWithOperations(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
) error {
	return serveWithAllOperations(ctx, socketPath, allowedGID, readDHCPStatus, staticWAN, nil)
}

func serveWithAllOperations(
	ctx context.Context,
	socketPath string,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
	pppoe pppoeOperations,
) error {
	if socketPath == "" {
		return errors.New("agent socket path is empty")
	}
	if ctx.Err() != nil {
		return nil
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on privileged agent socket: %w", err)
	}
	unixListener, ok := listener.(*net.UnixListener)
	if !ok {
		listener.Close()
		return errors.New("privileged agent requires a Unix stream socket")
	}
	unixListener.SetUnlinkOnClose(true)
	if err := os.Chmod(socketPath, 0660); err != nil {
		listener.Close()
		return fmt.Errorf("set privileged agent socket mode: %w", err)
	}
	defer listener.Close()
	serveDone := make(chan struct{})
	defer close(serveDone)

	go func() {
		select {
		case <-ctx.Done():
			listener.Close()
		case <-serveDone:
		}
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept privileged agent connection: %w", err)
		}
		handleConnectionWithAllOperations(connection, allowedGID, readDHCPStatus, staticWAN, pppoe)
	}
}

// SendRequest connects to the agent and sends one operation request.
func SendRequest(ctx context.Context, socketPath, operation string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: operation})
}

// SendDHCPStatus requests a filtered, read-only DHCP view for one interface.
func SendDHCPStatus(ctx context.Context, socketPath, interfaceName string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{
		Operation: "dhcp-status",
		Interface: interfaceName,
	})
}

// SendApplyStaticWAN sends a fully validated static profile to the privileged agent.
func SendApplyStaticWAN(ctx context.Context, socketPath string, config networkinventory.StaticWANConfig) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "static-wan-apply", Interface: networkinventory.StaticWANInterface, StaticWANConfig: &config})
}

// SendConfirmStaticWAN commits a verified static WAN transaction.
func SendConfirmStaticWAN(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "static-wan-confirm", TransactionID: transactionID})
}

// SendRollbackStaticWAN restores the previous WAN configuration immediately.
func SendRollbackStaticWAN(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "static-wan-rollback", TransactionID: transactionID})
}

func SendLAN(ctx context.Context, socketPath string, request Request) (Response, error) {
	return sendRequest(ctx, socketPath, request)
}

func SendWANLine(ctx context.Context, socketPath string, config *networkinventory.WANLineConfig) (Response, error) {
	operation := "wan-line-status"
	if config != nil {
		operation = "wan-line-apply"
	}
	return sendRequest(ctx, socketPath, Request{Operation: operation, WANLineConfig: config})
}

func SendApplyPPPoE(ctx context.Context, socketPath string, config networkinventory.PPPoEConfig) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-apply", Interface: networkinventory.StaticWANInterface, PPPoEConfig: &config})
}

func SendPPPoEStatus(ctx context.Context, socketPath string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-status"})
}

func SendConfirmPPPoE(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-confirm", TransactionID: transactionID})
}

func SendRollbackPPPoE(ctx context.Context, socketPath, transactionID string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-rollback", TransactionID: transactionID})
}

func SendReconnectPPPoE(ctx context.Context, socketPath string) (Response, error) {
	return sendRequest(ctx, socketPath, Request{Operation: "pppoe-reconnect"})
}

func sendRequest(ctx context.Context, socketPath string, request Request) (Response, error) {
	var empty Response
	connection, err := dialAgent(ctx, socketPath)
	if err != nil {
		return empty, fmt.Errorf("connect to privileged agent: %w", err)
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(connectionTimeout)); err != nil {
		return empty, fmt.Errorf("set privileged agent deadline: %w", err)
	}

	encoder := json.NewEncoder(connection)
	if err := encoder.Encode(request); err != nil {
		return empty, fmt.Errorf("send privileged agent request: %w", err)
	}
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		if err := unixConnection.CloseWrite(); err != nil {
			return empty, fmt.Errorf("finish privileged agent request: %w", err)
		}
	}

	responseLimit := int64(maxResponseBytes)
	if request.Operation == "traffic-status" {
		responseLimit = 1 << 20
	}
	decoder := json.NewDecoder(io.LimitReader(connection, responseLimit))
	decoder.DisallowUnknownFields()
	var response Response
	if err := decoder.Decode(&response); err != nil {
		return empty, fmt.Errorf("read privileged agent response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return empty, errors.New("privileged agent returned multiple responses")
		}
		return empty, fmt.Errorf("read privileged agent response trailer: %w", err)
	}
	return response, nil
}

func dialAgent(ctx context.Context, socketPath string) (net.Conn, error) {
	deadline := time.Now().Add(connectionTimeout)
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	var lastErr error
	for {
		connection, err := dialer.DialContext(ctx, "unix", socketPath)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if !errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, err
		}
		wait := time.Until(deadline)
		if wait <= 0 {
			return nil, lastErr
		}
		if wait > 25*time.Millisecond {
			wait = 25 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func handleConnection(
	connection net.Conn,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
) {
	handleConnectionWithOperations(connection, allowedGID, readDHCPStatus, nil)
}

func handleConnectionWithOperations(
	connection net.Conn,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
) {
	handleConnectionWithAllOperations(connection, allowedGID, readDHCPStatus, staticWAN, nil)
}

func handleConnectionWithAllOperations(
	connection net.Conn,
	allowedGID uint32,
	readDHCPStatus func(string) (networkinventory.DHCPStatus, error),
	staticWAN staticWANOperations,
	pppoe pppoeOperations,
) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(connectionTimeout))

	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		writeResponse(connection, Response{Error: "unauthorized"})
		return
	}
	peerGID, err := unixPeerGID(unixConnection)
	if err != nil || peerGID != allowedGID {
		writeResponse(connection, Response{Error: "unauthorized"})
		return
	}

	request, err := decodeRequest(connection)
	if err != nil {
		writeResponse(connection, Response{Error: "invalid_request"})
		return
	}
	if request.WANLineConfig != nil && request.Operation != "wan-line-apply" {
		writeResponse(connection, Response{Error: "invalid_request"})
		return
	}
	if request.DNSConfig != nil && request.Operation != "lan-dns-save" ||
		request.ProxyProfile != nil && request.Operation != "proxy-profile-apply" ||
		request.RoutingConfig != nil && request.Operation != "routing-apply" ||
		request.FirewallRules != nil && request.Operation != "firewall-rules-apply" ||
		request.ReservationChange != nil && request.Operation != "reservation-change" ||
		(request.LANConfig != nil && request.Operation != "lan-apply") ||
		(request.LANDestination != "" && request.Operation != "lan-confirm") {
		writeResponse(connection, Response{Error: "invalid_request"})
		return
	}
	switch request.Operation {
	case "proxy-profile-status", "proxy-profile-apply":
		if (request.Operation == "proxy-profile-apply") != (request.ProxyProfile != nil) ||
			request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil ||
			request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		if request.ProxyProfile != nil {
			if err := proxyProfiles.Apply(ctx, *request.ProxyProfile); err != nil {
				writeResponse(connection, Response{Error: err.Error()})
				return
			}
			writeResponse(connection, Response{ProxyProfile: &request.ProxyProfile.Profile})
			return
		}
		profile, err := proxyProfiles.Status()
		if err != nil {
			writeResponse(connection, Response{Error: err.Error()})
			return
		}
		writeResponse(connection, Response{ProxyProfile: &profile})
		return
	case "lan-status", "lan-apply", "lan-confirm", "reservation-list", "reservation-change", "lan-dns-status", "lan-dns-save", "lan-dns-clear",
		"firewall-status", "firewall-apply", "firewall-rules-status", "firewall-rules-apply", "routing-status", "routing-apply", "traffic-status":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.WANLineConfig != nil ||
			(request.Operation == "lan-apply") != (request.LANConfig != nil) ||
			(request.Operation == "lan-dns-save") != (request.DNSConfig != nil) ||
			(request.Operation == "firewall-rules-apply") != (request.FirewallRules != nil) ||
			(request.Operation == "routing-apply") != (request.RoutingConfig != nil) ||
			(request.Operation == "reservation-change") != (request.ReservationChange != nil) ||
			(request.Operation == "lan-confirm") != (request.TransactionID != "" && request.LANDestination != "") ||
			(request.Operation != "lan-confirm" && request.TransactionID != "") {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		manager, ok := staticWAN.(*networkinventory.StaticWANManager)
		if !ok {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		writeResponse(connection, handleLANRequest(ctx, manager, request))

	case "status":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		writeResponse(connection, Response{Status: "ok", Agent: "ready"})
	case "dhcp-status":
		if !networkinventory.ValidInterfaceName(request.Interface) || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		status, err := readDHCPStatus(request.Interface)
		if err != nil {
			writeResponse(connection, Response{Error: "network_status_unavailable"})
			return
		}
		writeResponse(connection, Response{DHCPStatus: &status})
	case "wan-probe-state":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		state, err := networkinventory.ReadWANProbeState(ctx)
		if err != nil {
			writeResponse(connection, Response{Error: "network_status_unavailable"})
			return
		}
		writeResponse(connection, Response{WANProbeState: &state})
	case "wan-line-status", "wan-line-apply":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil ||
			request.TransactionID != "" || (request.Operation == "wan-line-apply") != (request.WANLineConfig != nil) {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		manager, ok := staticWAN.(*networkinventory.StaticWANManager)
		if !ok {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		if request.WANLineConfig == nil {
			status, err := manager.WANLineStatus()
			if err != nil {
				writeResponse(connection, Response{Error: "wan_line_status_unavailable"})
				return
			}
			writeResponse(connection, Response{WANLineStatus: &status})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		transaction, err := manager.ApplyWANLine(ctx, *request.WANLineConfig)
		if err != nil {
			writeResponse(connection, Response{Error: "wan_line_apply_failed"})
			return
		}
		writeResponse(connection, Response{StaticWANTransaction: &transaction})
	case "static-wan-apply":
		if request.Interface != networkinventory.StaticWANInterface || request.StaticWANConfig == nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if err := networkinventory.ValidateStaticWAN(*request.StaticWANConfig); err != nil {
			writeResponse(connection, Response{Error: "invalid_static_config"})
			return
		}
		if staticWAN == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		transaction, err := staticWAN.Apply(ctx, *request.StaticWANConfig)
		if err != nil {
			writeResponse(connection, Response{Error: "static_wan_apply_failed"})
			return
		}
		writeResponse(connection, Response{StaticWANTransaction: &transaction})
	case "static-wan-confirm", "static-wan-rollback":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || !validTransactionID(request.TransactionID) {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if staticWAN == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		var err error
		result := "confirmed"
		if request.Operation == "static-wan-confirm" {
			err = staticWAN.Confirm(ctx, request.TransactionID)
		} else {
			result = "rolled_back"
			err = staticWAN.Rollback(ctx, request.TransactionID)
		}
		if err != nil {
			writeResponse(connection, Response{Error: "static_wan_transaction_failed"})
			return
		}
		writeResponse(connection, Response{Status: result})
	case "pppoe-status":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		status, err := pppoe.Status()
		if err != nil {
			writeResponse(connection, Response{Error: "pppoe_status_unavailable"})
			return
		}
		writeResponse(connection, Response{PPPoEStatus: &status})
	case "pppoe-apply":
		if request.Interface != networkinventory.StaticWANInterface || request.PPPoEConfig == nil || request.StaticWANConfig != nil || request.TransactionID != "" || networkinventory.ValidatePPPoEConfig(*request.PPPoEConfig) != nil {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		transaction, err := pppoe.Apply(ctx, *request.PPPoEConfig)
		if err != nil {
			writeResponse(connection, Response{Error: "pppoe_apply_failed"})
			return
		}
		writeResponse(connection, Response{PPPoETransaction: &transaction})
	case "pppoe-confirm", "pppoe-rollback":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || !validTransactionID(request.TransactionID) {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		result := "rolled_back"
		var err error
		if request.Operation == "pppoe-confirm" {
			result = "confirmed"
			err = pppoe.Confirm(ctx, request.TransactionID)
		} else {
			err = pppoe.Rollback(ctx, request.TransactionID)
		}
		if err != nil {
			writeResponse(connection, Response{Error: "pppoe_transaction_failed"})
			return
		}
		writeResponse(connection, Response{Status: result})
	case "pppoe-reconnect":
		if request.Interface != "" || request.StaticWANConfig != nil || request.PPPoEConfig != nil || request.TransactionID != "" {
			writeResponse(connection, Response{Error: "invalid_request"})
			return
		}
		if pppoe == nil {
			writeResponse(connection, Response{Error: "unsupported_operation"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
		defer cancel()
		if err := pppoe.Reconnect(ctx); err != nil {
			writeResponse(connection, Response{Error: "pppoe_reconnect_failed"})
			return
		}
		writeResponse(connection, Response{Status: "reconnecting"})
	default:
		writeResponse(connection, Response{Error: "unsupported_operation"})
	}
}

func validTransactionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, value := range id {
		if !(value >= '0' && value <= '9') && !(value >= 'a' && value <= 'f') {
			return false
		}
	}
	return true
}

func decodeRequest(reader io.Reader) (Request, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxRequestBytes+1))
	if err != nil || len(payload) == 0 || len(payload) > maxRequestBytes || payload[len(payload)-1] != '\n' {
		return Request{}, errors.New("request is incomplete or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var request Request
	if err := decoder.Decode(&request); err != nil {
		return Request{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Request{}, errors.New("request contains multiple JSON values")
		}
		return Request{}, err
	}
	if request.Operation == "" {
		return Request{}, errors.New("request operation is empty")
	}
	return request, nil
}

func writeResponse(writer io.Writer, response Response) {
	_ = json.NewEncoder(writer).Encode(response)
}

func handleLANRequest(ctx context.Context, manager *networkinventory.StaticWANManager, request Request) Response {
	switch request.Operation {
	case "traffic-status":
		snapshot, err := manager.Traffic(ctx)
		if err != nil {
			log.Printf("read traffic accounting: %v", err)
			return Response{Error: "traffic_unavailable"}
		}
		return Response{Traffic: &snapshot}
	case "routing-status":
		result, err := manager.RoutingStatus(ctx)
		if err != nil {
			return Response{Error: "routing_unavailable"}
		}
		return Response{RoutingStatus: &result}
	case "routing-apply":
		result, err := manager.ApplyRouting(ctx, *request.RoutingConfig)
		switch {
		case err == nil:
			return Response{StaticWANTransaction: &result}
		case errors.Is(err, networkinventory.ErrStaticWANPending):
			return Response{Error: "network_change_pending"}
		case errors.Is(err, networkinventory.ErrInvalidRouting),
			errors.Is(err, networkinventory.ErrRoutingOverlap),
			errors.Is(err, networkinventory.ErrRoutingGateway),
			errors.Is(err, networkinventory.ErrRoutingWAN),
			errors.Is(err, networkinventory.ErrRoutingResources):
			return Response{Error: err.Error()}
		default:
			log.Printf("apply routing: %v", err)
			return Response{Error: "routing_unavailable"}
		}
	case "firewall-rules-status":
		result, err := manager.FirewallRulesStatus(ctx)
		if err != nil {
			return Response{Error: "firewall_unavailable"}
		}
		return Response{RuleSettings: &result}
	case "firewall-rules-apply":
		result, err := manager.ApplyFirewallRules(ctx, *request.FirewallRules)
		switch {
		case err == nil:
			return Response{StaticWANTransaction: &result}
		case errors.Is(err, networkinventory.ErrStaticWANPending):
			return Response{Error: "network_change_pending"}
		case errors.Is(err, networkinventory.ErrInvalidFirewallRules):
			return Response{Error: "invalid_firewall_rules"}
		default:
			log.Printf("apply firewall rules: %v", err)
			return Response{Error: "firewall_unavailable"}
		}
	case "firewall-status":
		result, err := manager.FirewallStatus(ctx)
		if err != nil {
			return Response{Error: "firewall_unavailable"}
		}
		return Response{Firewall: &result}
	case "firewall-apply":
		result, err := manager.ApplyFirewall(ctx)
		if errors.Is(err, networkinventory.ErrStaticWANPending) {
			return Response{Error: "network_change_pending"}
		}
		if err != nil {
			log.Printf("apply firewall: %v", err)
			return Response{Error: "firewall_unavailable"}
		}
		return Response{StaticWANTransaction: &result}
	case "lan-dns-status":
		result, err := manager.LANDNSStatus()
		if err != nil {
			return Response{Error: "lan_dns_unavailable"}
		}
		return Response{DNSStatus: &result}
	case "lan-dns-save":
		return dnsChangeResponse(manager.ApplyLANDNS(ctx, *request.DNSConfig))
	case "lan-dns-clear":
		return dnsChangeResponse(manager.ClearLANDNSCache(ctx))
	case "reservation-list":
		result, err := manager.ListDHCPReservations()
		if err != nil {
			return Response{Error: "reservations_unavailable"}
		}
		return Response{Reservations: &result}
	case "reservation-change":
		err := manager.ChangeDHCPReservation(ctx, *request.ReservationChange)
		switch {
		case err == nil:
			return Response{Status: "saved"}
		case errors.Is(err, networkinventory.ErrStaticWANPending):
			return Response{Error: "network_change_pending"}
		case errors.Is(err, networkinventory.ErrReservationInvalid),
			errors.Is(err, networkinventory.ErrReservationConflict),
			errors.Is(err, networkinventory.ErrReservationMissing),
			errors.Is(err, networkinventory.ErrReservationLimit):
			return Response{Error: err.Error()}
		default:
			log.Printf("change DHCP reservation: %v", err)
			return Response{Error: "reservations_unavailable"}
		}
	case "lan-status":
		status, err := manager.LANStatus(ctx)
		if err != nil {
			return Response{Error: "lan_status_unavailable"}
		}
		return Response{LANStatus: &status}
	case "lan-apply":
		transaction, err := manager.ApplyLAN(ctx, *request.LANConfig)
		if err != nil {
			var conflict *networkinventory.LANConflict
			if errors.As(err, &conflict) {
				return Response{Error: "lan_overlap", LANConflict: conflict}
			}
			if errors.Is(err, networkinventory.ErrInvalidLANDNS) {
				return Response{Error: "lan_dns_address_conflict"}
			}
			if errors.Is(err, networkinventory.ErrInvalidFirewallRules) {
				return Response{Error: "lan_firewall_target_conflict"}
			}
			if errors.Is(err, networkinventory.ErrRoutingOverlap) {
				return Response{Error: "lan_routing_conflict"}
			}
			if errors.Is(err, networkinventory.ErrReservationConflict) {
				return Response{Error: "lan_reservation_conflict"}
			}
			if errors.Is(err, networkinventory.ErrStaticWANPending) {
				return Response{Error: "network_change_pending"}
			}
			log.Printf("apply LAN settings: %v", err)
			return Response{Error: "lan_apply_failed"}
		}
		return Response{StaticWANTransaction: &transaction}
	case "lan-confirm":
		if err := manager.ConfirmLAN(ctx, request.TransactionID, request.LANDestination); err != nil {
			return Response{Error: "lan_confirmation_failed"}
		}
		return Response{Status: "confirmed"}
	default:
		return Response{Error: "invalid_request"}
	}
}

func dnsChangeResponse(err error) Response {
	switch {
	case err == nil:
		return Response{Status: "saved"}
	case errors.Is(err, networkinventory.ErrInvalidLANDNS):
		return Response{Error: err.Error()}
	case errors.Is(err, networkinventory.ErrStaticWANPending):
		return Response{Error: "network_change_pending"}
	default:
		log.Printf("change LAN DNS: %v", err)
		return Response{Error: "lan_dns_unavailable"}
	}
}
