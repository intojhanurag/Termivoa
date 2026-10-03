package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateComputerOrigin(t *testing.T) {
	for _, origin := range []string{"https://mac.tailx.ts.net", "https://a.bc", "https://xn--bcher-kva.tailx.ts.net"} {
		if err := ValidateComputerOrigin(origin); err != nil {
			t.Fatalf("%s rejected: %v", origin, err)
		}
	}
	for _, origin := range []string{
		"http://mac.tailx.ts.net", "https://mac.tailx.ts.net:443", "https://mac.tailx.ts.net.", "https://Mac.tailx.ts.net",
		"https://mac.tailx.ts.net/", "https://user@mac.tailx.ts.net", "https://mac", "https://-mac.tailx.ts.net",
		"https://mac..ts.net", "null", "https://127.0.0.1:8787", "https://" + strings.Repeat("a", 64) + ".ts.net",
	} {
		if err := ValidateComputerOrigin(origin); err == nil {
			t.Fatalf("%s accepted", origin)
		}
	}
}

func TestGatewayRPCOriginIsForwardedOnRequestsOnly(t *testing.T) {
	withOrigin := func(messageType string, payload any, origin string) []byte {
		var message map[string]any
		if err := json.Unmarshal(rpcJSON(t, messageType, payload), &message); err != nil {
			t.Fatal(err)
		}
		message["origin"] = origin
		data, _ := json.Marshal(message)
		return data
	}
	credential := GatewayRPCCredentialPayload{Credential: rpcTestSecret}
	envelope, err := DecodeGatewayRPCRequest(withOrigin("gateway.settings", credential, "https://mac.tailx.ts.net"))
	if err != nil || envelope.Origin == nil || *envelope.Origin != "https://mac.tailx.ts.net" {
		t.Fatalf("origin = %v, %v", envelope.Origin, err)
	}
	redeem := HTTPPairRedeemRequest{Grant: rpcTestSecret, DeviceLabel: "Phone"}
	if _, err := DecodeGatewayRPCRequest(withOrigin("gateway.pair.redeem", redeem, "https://mac.tailx.ts.net")); err != nil {
		t.Fatal(err)
	}
	for name, message := range map[string][]byte{
		"null origin":      withOrigin("gateway.settings", credential, "null"),
		"origin with path": withOrigin("gateway.settings", credential, "https://mac.tailx.ts.net/x"),
		"on a response":    withOrigin("gateway.auth.logout.result", BrokerRPCEmptyPayload{}, "https://mac.tailx.ts.net"),
	} {
		if _, err := DecodeGatewayRPCRequest(message); err == nil {
			if _, err := DecodeGatewayRPCResponse(message); err == nil {
				t.Fatalf("%s accepted", name)
			}
		}
	}
	if _, err := DecodeAdminRPCRequest(withOrigin("admin.device.list", BrokerRPCEmptyPayload{}, "https://mac.tailx.ts.net")); err == nil {
		t.Fatal("admin request with an origin accepted")
	}
}

func TestGatewayComputerMessages(t *testing.T) {
	add := GatewayRPCComputerAddPayload{Credential: rpcTestSecret, Request: HTTPComputerAddRequest{
		Origin: "https://vps.tailx.ts.net", Label: "vps", State: HTTPComputerStatePending,
	}}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.computers.add", add)); err != nil {
		t.Fatal(err)
	}
	add.Request.State = "unknown"
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.computers.add", add)); err == nil {
		t.Fatal("unknown computer state accepted")
	}
	remove := GatewayRPCComputerRemovePayload{Credential: rpcTestSecret, Origin: "https://vps.tailx.ts.net:443"}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.computers.remove", remove)); err == nil {
		t.Fatal("explicit port accepted")
	}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.computers.list", GatewayRPCCredentialPayload{Credential: rpcTestSecret})); err != nil {
		t.Fatal(err)
	}
	list := GatewayRPCComputerListResultPayload{
		Computers:      []HTTPComputer{{Origin: "https://vps.tailx.ts.net", Label: "vps"}},
		PendingOrigins: []string{"https://linux.tailx.ts.net"},
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.list.result", list)); err != nil {
		t.Fatal(err)
	}
	list.PendingOrigins = append(list.PendingOrigins, list.PendingOrigins[0])
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.list.result", list)); err == nil {
		t.Fatal("duplicate pending origin accepted")
	}
	owner := "ada@example.com"
	added := HTTPComputerAddResponse{
		Computer: HTTPComputer{Origin: "https://vps.tailx.ts.net", Label: "vps"}, State: Confirmed,
		Node: TailnetNode{ID: "nABC123CNTRL", Owner: &owner, Tags: []string{}},
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.add.result", added)); err != nil {
		t.Fatal(err)
	}
	added.Node.Tags = []string{"tag:has space"}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.add.result", added)); err == nil {
		t.Fatal("tag with a space accepted")
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.remove.result", BrokerRPCEmptyPayload{})); err != nil {
		t.Fatal(err)
	}
	probe := GatewayRPCOriginResultPayload{BoundOrigins: []string{"https://mac.tailx.ts.net"}, PairingOrigins: []string{"https://mac.tailx.ts.net"}}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.origin.result", probe)); err != nil {
		t.Fatal(err)
	}
	probe.BoundOrigins = make([]string, MaxBoundOrigins+1)
	for index := range probe.BoundOrigins {
		probe.BoundOrigins[index] = "https://m" + strings.Repeat("a", index) + ".tailx.ts.net"
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.origin.result", probe)); err == nil {
		t.Fatal("65 bound origins accepted")
	}
}

func TestSettingsCarryProtocolRevision(t *testing.T) {
	settings := HTTPSettingsResponse{
		BrokerGeneration: rpcTestID, Device: HTTPDeviceSummary{ID: rpcTestID, Label: "Phone"}, HostBuild: "test",
		Projects: []HTTPNamedOption{}, ShellProfiles: []HTTPNamedOption{},
		ProtocolRevision: ProtocolRevision, MinProtocolRevision: MinProtocolRevision,
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.settings.result", settings)); err != nil {
		t.Fatal(err)
	}
	settings.MinProtocolRevision = settings.ProtocolRevision + 1
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.settings.result", settings)); err == nil {
		t.Fatal("minimum above the revision accepted")
	}
}

func TestHostBuildIsPlainText(t *testing.T) {
	for _, build := range []string{"private-dev", "0.1.0-beta.2+1a2b3c4d5e6f.dirty", strings.Repeat("a", 128)} {
		if !ValidHostBuild(build) {
			t.Fatalf("%q rejected", build)
		}
	}
	for _, build := range []string{"", " 1.0", "1.0\n", "<b>1</b>", "v1\u202e", "é", strings.Repeat("a", 129)} {
		if ValidHostBuild(build) {
			t.Fatalf("%q accepted", build)
		}
	}
}

func TestAdminComputerMessages(t *testing.T) {
	via, allow := "mac", true
	for _, payload := range []AdminRPCPairGrantPayload{{}, {Via: &via}, {Via: &via, AllowTagged: &allow}} {
		if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.pair.grant", payload)); err != nil {
			t.Fatalf("%+v: %v", payload, err)
		}
	}
	bad := "Mac"
	for _, payload := range []AdminRPCPairGrantPayload{{Via: &bad}, {AllowTagged: &allow}} {
		if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.pair.grant", payload)); err == nil {
			t.Fatalf("%+v accepted", payload)
		}
	}
	app := "https://mac.tailx.ts.net"
	node := &TailnetNode{ID: "nABC", Tags: []string{"tag:server"}}
	grant := AdminRPCPairGrantResultPayload{Grant: rpcTestSecret, ExpiresAt: "2026-10-02T10:00:00Z", AppOrigin: &app, AppNode: node}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.grant.result", grant)); err != nil {
		t.Fatal(err)
	}
	grant.AppOrigin = nil
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.grant.result", grant)); err == nil {
		t.Fatal("appNode without appOrigin accepted")
	}
	if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.device.revoke.via", AdminRPCRevokeViaPayload{AppOrigin: app})); err != nil {
		t.Fatal(err)
	}
	revoked := AdminRPCRevokeViaResultPayload{AppOrigin: app, DeviceIDS: []string{rpcTestID}, ClosedStreams: 2}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.device.revoke.via.result", revoked)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.computers.list", BrokerRPCEmptyPayload{})); err != nil {
		t.Fatal(err)
	}
	computers := AdminRPCComputerListResultPayload{Computers: []AdminRPCComputer{{
		DeviceID: rpcTestID, Origin: "https://vps.tailx.ts.net", Label: "vps", CreatedAt: "2026-10-02T10:00:00Z",
	}}}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.computers.list.result", computers)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.computers.remove", AdminRPCComputerRemovePayload{Origin: "https://vps.tailx.ts.net"})); err != nil {
		t.Fatal(err)
	}
	removed := AdminRPCComputerRemoveResultPayload{Origin: "https://vps.tailx.ts.net", Removed: 1}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.computers.remove.result", removed)); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAttachThumbnailMode(t *testing.T) {
	for rate, valid := range map[int64]bool{1: true, 5: true, 30: true, 0: false, 31: false} {
		payload := SessionAttachPayload{Thumbnail: &ThumbnailMode{MaxUpdatesPerSecond: rate}}
		_, err := DecodeClient(encodeTestMessage(t, SessionAttach, payload, true))
		if (err == nil) != valid {
			t.Fatalf("rate %d: %v", rate, err)
		}
	}
	if _, err := DecodeClient(encodeTestMessage(t, SessionAttach, EmptyPayload{}, true)); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayComputerOfferAndFoundMessages(t *testing.T) {
	offer := HTTPComputerOfferRequest{Origin: "https://vps.tailx.ts.net", Grant: rpcTestSecret}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.computers.offer", offer)); err != nil {
		t.Fatal(err)
	}
	for name, message := range map[string][]byte{
		"explicit port":   rpcJSON(t, "gateway.computers.offer", HTTPComputerOfferRequest{Origin: "https://vps.tailx.ts.net:443", Grant: rpcTestSecret}),
		"http origin":     rpcJSON(t, "gateway.computers.offer", HTTPComputerOfferRequest{Origin: "http://vps.tailx.ts.net", Grant: rpcTestSecret}),
		"short grant":     rpcJSON(t, "gateway.computers.offer", HTTPComputerOfferRequest{Origin: offer.Origin, Grant: rpcTestSecret[:42]}),
		"padded grant":    rpcJSON(t, "gateway.computers.offer", HTTPComputerOfferRequest{Origin: offer.Origin, Grant: rpcTestSecret[:42] + "="}),
		"missing grant":   rpcJSON(t, "gateway.computers.offer", map[string]string{"origin": offer.Origin}),
		"unknown field":   rpcJSON(t, "gateway.computers.offer", map[string]string{"origin": offer.Origin, "grant": rpcTestSecret, "label": "vps"}),
		"found no cookie": rpcJSON(t, "gateway.computers.found", BrokerRPCEmptyPayload{}),
	} {
		if _, err := DecodeGatewayRPCRequest(message); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if err := ValidateComputerOffer(offer); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.offer.result", BrokerRPCEmptyPayload{})); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGatewayRPCRequest(rpcJSON(t, "gateway.computers.found", GatewayRPCCredentialPayload{Credential: rpcTestSecret})); err != nil {
		t.Fatal(err)
	}

	entry := func(origin string) HTTPFoundComputer {
		return HTTPFoundComputer{Origin: origin, Label: "vps", Grant: rpcTestSecret, ExpiresAt: "2026-10-03T10:00:00Z"}
	}
	full := HTTPComputerFoundResponse{Computers: make([]HTTPFoundComputer, MaxFoundComputers)}
	for index := range full.Computers {
		full.Computers[index] = entry("https://m" + strings.Repeat("a", index) + ".tailx.ts.net")
	}
	for name, found := range map[string]HTTPComputerFoundResponse{
		"empty": {Computers: []HTTPFoundComputer{}},
		"two":   {Computers: []HTTPFoundComputer{entry("https://linux.tailx.ts.net"), entry("https://vps.tailx.ts.net")}},
		"16":    full,
	} {
		if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.found.result", found)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	changed := func(change func(*HTTPFoundComputer)) HTTPComputerFoundResponse {
		computer := entry("https://vps.tailx.ts.net")
		change(&computer)
		return HTTPComputerFoundResponse{Computers: []HTTPFoundComputer{computer}}
	}
	for name, found := range map[string]HTTPComputerFoundResponse{
		"nil list":      {},
		"17":            {Computers: append(append([]HTTPFoundComputer(nil), full.Computers...), entry("https://zz.tailx.ts.net"))},
		"unsorted":      {Computers: []HTTPFoundComputer{entry("https://vps.tailx.ts.net"), entry("https://linux.tailx.ts.net")}},
		"duplicate":     {Computers: []HTTPFoundComputer{entry("https://vps.tailx.ts.net"), entry("https://vps.tailx.ts.net")}},
		"control label": changed(func(computer *HTTPFoundComputer) { computer.Label = "vps\x1b[2J" }),
		"long label":    changed(func(computer *HTTPFoundComputer) { computer.Label = strings.Repeat("a", MaxComputerLabelBytes+1) }),
		"empty label":   changed(func(computer *HTTPFoundComputer) { computer.Label = "" }),
		"bad time":      changed(func(computer *HTTPFoundComputer) { computer.ExpiresAt = "soon" }),
		"bad grant":     changed(func(computer *HTTPFoundComputer) { computer.Grant = "short" }),
		"bad origin":    changed(func(computer *HTTPFoundComputer) { computer.Origin = "https://VPS.tailx.ts.net" }),
	} {
		if _, err := DecodeGatewayRPCResponse(rpcJSON(t, "gateway.computers.found.result", found)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestAdminPairOfferMessages(t *testing.T) {
	for _, via := range []string{"mac", "https://mac.tailx.ts.net"} {
		if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.pair.offer", AdminRPCPairOfferPayload{Via: via})); err != nil {
			t.Fatalf("%s: %v", via, err)
		}
	}
	for name, message := range map[string][]byte{
		"no via":       rpcJSON(t, "admin.pair.offer", BrokerRPCEmptyPayload{}),
		"bad via":      rpcJSON(t, "admin.pair.offer", AdminRPCPairOfferPayload{Via: "Mac"}),
		"allow tagged": rpcJSON(t, "admin.pair.offer", map[string]any{"via": "mac", "allowTagged": true}),
		"end bad":      rpcJSON(t, "admin.pair.offer.end", AdminRPCPairOfferEndPayload{Grant: new("short")}),
		"end unknown":  rpcJSON(t, "admin.pair.offer.end", map[string]string{"via": "mac"}),
	} {
		if _, err := DecodeAdminRPCRequest(message); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	for _, end := range []AdminRPCPairOfferEndPayload{{}, {Grant: new(rpcTestSecret)}} {
		if _, err := DecodeAdminRPCRequest(rpcJSON(t, "admin.pair.offer.end", end)); err != nil {
			t.Fatalf("%+v: %v", end, err)
		}
	}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.offer.end.result", BrokerRPCEmptyPayload{})); err != nil {
		t.Fatal(err)
	}

	result := func(delivered bool, problem *AdminRPCPairOfferProblem) AdminRPCPairOfferResultPayload {
		return AdminRPCPairOfferResultPayload{
			AppOrigin: "https://mac.tailx.ts.net", AppNode: TailnetNode{ID: "nMAC", Owner: new("ada@example.com"), Tags: []string{}},
			ExpiresAt: "2026-10-03T10:02:00Z", Delivered: delivered, Problem: problem,
		}
	}
	unreachable, busy, notAccepted, unknown := Unreachable, Busy, NotAccepted, AdminRPCPairOfferProblem("refused")
	for _, payload := range []AdminRPCPairOfferResultPayload{result(true, nil), result(false, &unreachable), result(false, &busy), result(false, &notAccepted)} {
		if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.offer.result", payload)); err != nil {
			t.Fatalf("%+v: %v", payload, err)
		}
	}
	badOrigin, badTime := result(true, nil), result(true, nil)
	badOrigin.AppOrigin = "https://mac.tailx.ts.net:443"
	badTime.ExpiresAt = "later"
	for name, payload := range map[string]AdminRPCPairOfferResultPayload{
		"delivered with problem": result(true, &unreachable),
		"undelivered no problem": result(false, nil),
		"unknown problem":        result(false, &unknown),
		"app origin with port":   badOrigin,
		"bad expiry":             badTime,
	} {
		if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.offer.result", payload)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	withGrant := map[string]any{
		"appOrigin": "https://mac.tailx.ts.net", "appNode": result(true, nil).AppNode,
		"expiresAt": "2026-10-03T10:02:00Z", "delivered": true, "grant": rpcTestSecret,
	}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.pair.offer.result", withGrant)); err == nil {
		t.Fatal("offer result with the grant accepted")
	}
	homeChanged := BrokerRPCErrorPayload{Code: HomeChanged, Message: "mac is now a different Tailscale machine"}
	if _, err := DecodeAdminRPCResponse(rpcJSON(t, "admin.error", homeChanged)); err != nil {
		t.Fatal(err)
	}
}
