package domain

import (
	"testing"
)

var (
	esp = "/devices/" + idESP
	lab = "/devices/" + idLab
)

func TestDeviceRules(t *testing.T) {
	runMutations(t, []mutation{
		{"two devices with one MAC", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"macs":["24:0a:c4:00:00:42"]}`), "devices", idLab, "identifiers")
		}, lab + "/identifiers/macs/0", CodeDuplicateIdentifier},
		{"device with a multicast MAC", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"macs":["01:00:5e:00:00:01"]}`), "devices", idESP, "identifiers")
		}, esp + "/identifiers/macs/0", CodeInvalidMAC},
		{"device address equals a WireGuard client address", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"ipv4":["10.99.0.2"]}`), "devices", idLab, "identifiers")
		}, lab + "/identifiers/ipv4/0", CodeDuplicateIdentifier},
		{"two devices with the same range", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"ipv4":["10.50.0.0/24"]}`), "devices", idESP, "identifiers")
			d.set(t, obj(t, `{"ipv4":["10.50.0.0/24"]}`), "devices", idLab, "identifiers")
		}, lab + "/identifiers/ipv4/0", CodeDuplicateIdentifier},
		{"two devices with the same address", func(t *testing.T, d doc) {
			d.del(t, "devices", idESP, "fixed_ip")
			d.set(t, obj(t, `{"ipv4":["10.50.0.10"]}`), "devices", idESP, "identifiers")
		}, lab + "/identifiers/ipv4/0", CodeDuplicateIdentifier},
		{"device range with host bits", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"ipv4":["10.50.0.1/24"]}`), "devices", idLab, "identifiers")
		}, lab + "/identifiers/ipv4/0", CodeHostBitsSet},
		{"fixed address without a network", func(t *testing.T, d doc) { d.del(t, "devices", idESP, "network") }, esp + "/fixed_ip", CodeFixedIPRequires},
		{"fixed address in a WireGuard network", func(t *testing.T, d doc) { d.set(t, "lab-hub", "devices", idESP, "network") }, esp + "/fixed_ip", CodeFixedIPRequires},
		{"fixed address without a MAC", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"ipv4":["10.10.0.42"]}`), "devices", idESP, "identifiers")
		}, esp + "/fixed_ip", CodeFixedIPRequires},
		{"fixed address outside the subnet", func(t *testing.T, d doc) { d.set(t, "10.20.0.42", "devices", idESP, "fixed_ip") }, esp + "/fixed_ip", CodeOutsideSubnet},
		{"fixed address is the gateway", func(t *testing.T, d doc) { d.set(t, "10.10.0.1", "devices", idESP, "fixed_ip") }, esp + "/fixed_ip", CodeDuplicateAddress},
		{"fixed address is the broadcast address", func(t *testing.T, d doc) { d.set(t, "10.10.0.255", "devices", idESP, "fixed_ip") }, esp + "/fixed_ip", CodeOutsideSubnet},
		{"two devices with one reservation", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"other","identifiers":{"macs":["24:0a:c4:00:00:43"]},"network":"IoT","fixed_ip":"10.10.0.42"}`), "devices", idNew)
		}, "/devices/" + idNew + "/fixed_ip", CodeDuplicateAddress},

		{"the same device twice in a group", func(t *testing.T, d doc) {
			d.set(t, []any{"esp32-42", "esp32-42"}, "groups", idSensors, "members")
		}, "/groups/" + idSensors + "/members/1", CodeDuplicateMember},
		{"the same device twice in a group by name and UUID", func(t *testing.T, d doc) {
			d.set(t, []any{"esp32-42", idESP}, "groups", idSensors, "members")
		}, "/groups/" + idSensors + "/members/1", CodeDuplicateMember},
		{"a group member that does not exist", func(t *testing.T, d doc) {
			d.set(t, []any{"ghost"}, "groups", idSensors, "members")
		}, "/groups/" + idSensors + "/members/0", CodeUnknownReference},
		{"a probe in a WireGuard network", func(t *testing.T, d doc) { d.set(t, "lab-hub", "probes", idProbe, "network") }, "/probes/" + idProbe + "/network", CodeProbeNetwork},

		{"matrix entry from an endpoint to itself", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"from":{"network":"IoT"},"to":{"network":"IoT"},"policy":"allow"}`)}, "access_matrix", "entries")
		}, "/access_matrix/entries/0", CodeMatrixSelf},
		{"matrix pair set twice", func(t *testing.T, d doc) {
			d.set(t, []any{
				obj(t, `{"from":{"network":"lab-hub"},"to":{"network":"IoT"},"policy":"allow"}`),
				obj(t, `{"from":{"network":"lab-hub"},"to":{"network":"IoT"},"policy":"deny"}`),
			}, "access_matrix", "entries")
		}, "/access_matrix/entries/1", CodeMatrixDuplicate},
		{"matrix names the same network by name and UUID", func(t *testing.T, d doc) {
			d.set(t, []any{
				obj(t, `{"from":{"network":"lab-hub"},"to":{"network":"IoT"},"policy":"allow"}`),
				obj(t, `{"from":{"network":"`+idHub+`"},"to":{"network":"`+idIoT+`"},"policy":"deny"}`),
			}, "access_matrix", "entries")
		}, "/access_matrix/entries/1", CodeMatrixDuplicate},
		{"matrix endpoint that does not exist", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"from":{"network":"nowhere"},"to":{"uplink":true},"policy":"allow"}`)}, "access_matrix", "entries")
		}, "/access_matrix/entries/0/from/network", CodeUnknownReference},
		{"matrix client endpoint that is a network", func(t *testing.T, d doc) {
			d.set(t, []any{obj(t, `{"from":{"client":"IoT"},"to":{"uplink":true},"policy":"allow"}`)}, "access_matrix", "entries")
		}, "/access_matrix/entries/0/from/client", CodeUnknownReference},
	})
}

func TestMatrixToTheUplinkAndManagementIsFine(t *testing.T) {
	d := baseDoc(t)
	d.set(t, []any{
		obj(t, `{"from":{"client":"lab-rA"},"to":{"uplink":true},"policy":"allow"}`),
		obj(t, `{"from":{"management":true},"to":{"network":"IoT"},"policy":"allow"}`),
	}, "access_matrix", "entries")
	wantValid(t, d.validate(t))
}

func TestNamesAndKeys(t *testing.T) {
	runMutations(t, []mutation{
		{"two networks with one name, ignoring case", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"type":"lan","name":"iot","interfaces":[{"name":"lan1"}],"address":"10.30.0.1/24"}`), "networks", idNew)
		}, "/networks/" + idNew + "/name", CodeDuplicateName},
		{"a name that looks like a UUID", func(t *testing.T, d doc) { d.set(t, idNew, "devices", idESP, "name") }, esp + "/name", CodeNameIsUUID},
		{"a probe named like a device", func(t *testing.T, d doc) { d.set(t, "esp32-42", "probes", idProbe, "name") }, "/probes/" + idProbe + "/name", CodeDuplicateName},
		{"a probe named like a WireGuard client", func(t *testing.T, d doc) { d.set(t, "lab-rA", "probes", idProbe, "name") }, "/probes/" + idProbe + "/name", CodeDuplicateName},
		{"a probe with the UUID of a device", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"p2","network":"IoT"}`), "probes", idESP)
		}, "/probes/" + idESP, CodeDuplicateID},
		{"a group named like a group", func(t *testing.T, d doc) { d.set(t, "G2", "groups", idSensors, "name") }, "/groups/" + idG2 + "/name", CodeDuplicateName},
		{"a profile with the name of a built-in profile", func(t *testing.T, d doc) { d.set(t, "Bad-LTE", "profiles", idProfile, "name") }, "/profiles/" + idProfile + "/name", CodeReservedName},
		{"two scenarios with one name", func(t *testing.T, d doc) {
			d.set(t, d.node(t, "scenarios", idScenario), "scenarios", idNew)
		}, "/scenarios/" + idNew + "/name", CodeDuplicateName},
		{"two faults with one name", func(t *testing.T, d doc) {
			d.set(t, "iot-latency", "faults", idFaultDev, "name")
		}, "/faults/" + idFaultDev + "/name", CodeDuplicateName},
		{"two protocols with one name", func(t *testing.T, d doc) {
			p := obj(t, `{"name":"bgp-site-b","type":"ospf","link":"site-b"}`)
			d.set(t, p, "routing", "protocols", idNew)
		}, "/routing/protocols/" + idNew + "/name", CodeDuplicateName},
		{"a key that is not a UUID", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"x"}`), "groups", "not-a-uuid")
		}, "/groups/not-a-uuid", CodeInvalidID},
		{"a key in upper case", func(t *testing.T, d doc) {
			d.set(t, obj(t, `{"name":"x"}`), "groups", "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE")
		}, "/groups/AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE", CodeInvalidID},
		{"a device that does not exist as a probe's network", func(t *testing.T, d doc) { d.set(t, "nowhere", "probes", idProbe, "network") }, "/probes/" + idProbe + "/network", CodeUnknownReference},
		{"a device network that does not exist", func(t *testing.T, d doc) { d.set(t, "nowhere", "devices", idESP, "network") }, esp + "/network", CodeUnknownReference},
	})
}

func TestReferencesMayBeNamesOrUUIDsInAnyCase(t *testing.T) {
	d := baseDoc(t)
	d.set(t, "IOT", "devices", idESP, "network") // a name, other case
	d.set(t, idHub, "access_matrix", "entries", 0, "from", "network")
	d.set(t, []any{"ESP32-42", idLab}, "groups", idSensors, "members")
	wantValid(t, d.validate(t))
}

func TestNormalizeReplacesEveryNameByItsUUID(t *testing.T) {
	cfg := exampleConfiguration(t)
	out, errs := Normalize(cfg)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	// spot checks across the document: scopes, matrix, routing, groups, scenarios, devices
	if got := *deref(out.Devices)[idESP].Network; got != idIoT {
		t.Errorf("device network = %s", got)
	}
	if got := (*deref(out.Groups)[idSensors].Members)[0]; got != idESP {
		t.Errorf("group member = %s", got)
	}
	if got := *deref(out.Faults)[idFaultNet].Source.Network; got != idIoT {
		t.Errorf("fault source = %s", got)
	}
	if got := *deref(out.Faults)[idFaultTun].Tunnel.Link; got != idLink {
		t.Errorf("tunnel link = %s", got)
	}
	if got := *(*out.AccessMatrix.Entries)[0].From.Network; got != idHub {
		t.Errorf("matrix from = %s", got)
	}
	proto := deref(out.Routing.Protocols)[idProtocol]
	if proto.Link != idLink || *(*proto.Announce)[0].Network != idIoT || *(*proto.Announce)[1].Client != idClient {
		t.Errorf("protocol = %+v", proto)
	}
	if got := *deref(out.Scenarios)[idScenario].Target.Device; got != idESP {
		t.Errorf("scenario target = %s", got)
	}
	// the built-in profile "normal" is referenced by its fixed UUID
	if got := *deref(out.Scenarios)[idScenario].Steps[0].Profile; got != "5b455ed0-e2bf-5910-8dc6-7a0af428025c" {
		t.Errorf("step profile = %s", got)
	}
	wg, _ := deref(out.Networks)[idHub].AsWireGuardNetwork()
	if got := *(*deref(wg.Clients)[idClient].Reachable)[0].Network; got != idIoT {
		t.Errorf("client reachable = %s", got)
	}
	// the input is untouched
	if got := *deref(cfg.Devices)[idESP].Network; got != "IoT" {
		t.Errorf("Normalize changed its input: %s", got)
	}
	// and the result is stable: normalizing again changes nothing
	again, errs := Normalize(out)
	if len(errs) != 0 || !equalJSON(t, out, again) {
		t.Errorf("Normalize is not idempotent: %v", errs)
	}
}

func TestNormalizeReportsUnknownReferencesAndKeepsThem(t *testing.T) {
	cfg := exampleConfiguration(t)
	cfg2, _ := Normalize(cfg)
	devs := deref(cfg2.Devices)
	d := devs[idESP]
	bad := "nowhere"
	d.Network = &bad
	devs[idESP] = d
	out, errs := Normalize(cfg2)
	if !ValidationErrors(errs).Has(esp+"/network", CodeUnknownReference) {
		t.Fatalf("errors: %v", errs)
	}
	if got := *deref(out.Devices)[idESP].Network; got != "nowhere" {
		t.Errorf("an unresolved reference must stay: %s", got)
	}
}
