package network

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadInterfacesFromReportsLinkStateAndSpeed(t *testing.T) {
	root := t.TempDir()
	writeInterface(t, root, "lo", map[string]string{"operstate": "unknown", "carrier": "1", "speed": "10000"})
	writeInterface(t, root, "enp1s0", map[string]string{"operstate": "up", "carrier": "1", "speed": "1000"})
	writeInterface(t, root, "enp2s0", map[string]string{"operstate": "down", "carrier": "0", "speed": "-1"})
	writeInterface(t, root, "wlp3s0", map[string]string{"operstate": "up", "carrier": "1", "speed": "-1"})

	interfaces, err := ReadInterfacesFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 3 {
		t.Fatalf("got %d interfaces, want 3: %#v", len(interfaces), interfaces)
	}
	if interfaces[0].Name != "enp1s0" || interfaces[1].Name != "enp2s0" || interfaces[2].Name != "wlp3s0" {
		t.Fatalf("interfaces are not sorted by name: %#v", interfaces)
	}
	if interfaces[0].OperationalState != "up" || interfaces[0].Carrier == nil || !*interfaces[0].Carrier || interfaces[0].SpeedMbps == nil || *interfaces[0].SpeedMbps != 1000 {
		t.Fatalf("unexpected linked Ethernet interface: %#v", interfaces[0])
	}
	if interfaces[1].OperationalState != "down" || interfaces[1].Carrier == nil || *interfaces[1].Carrier || interfaces[1].SpeedMbps != nil {
		t.Fatalf("unexpected disconnected Ethernet interface: %#v", interfaces[1])
	}
	if interfaces[2].OperationalState != "up" || interfaces[2].Carrier == nil || !*interfaces[2].Carrier || interfaces[2].SpeedMbps != nil {
		t.Fatalf("unknown wireless speed was not represented as unknown: %#v", interfaces[2])
	}
}

func TestReadInterfacesFromHandlesUnavailableProperties(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "eth0"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "eth1"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "eth1", "operstate"), []byte("up\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "eth1", "carrier"), []byte("unknown\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "eth1", "speed"), []byte("not-a-number\n"), 0600); err != nil {
		t.Fatal(err)
	}

	interfaces, err := ReadInterfacesFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 2 || interfaces[0].OperationalState != "unknown" || interfaces[0].Carrier != nil || interfaces[0].SpeedMbps != nil {
		t.Fatalf("missing sysfs values were not represented safely: %#v", interfaces)
	}
	if interfaces[1].OperationalState != "up" || interfaces[1].Carrier != nil || interfaces[1].SpeedMbps != nil {
		t.Fatalf("invalid sysfs values were not represented safely: %#v", interfaces[1])
	}
}

func TestReadInterfacesFromRejectsMissingDirectory(t *testing.T) {
	_, err := ReadInterfacesFrom(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("missing sysfs directory did not return an error")
	}
}

func writeInterface(t *testing.T, root, name string, values map[string]string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	for field, value := range values {
		if err := os.WriteFile(filepath.Join(path, field), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
