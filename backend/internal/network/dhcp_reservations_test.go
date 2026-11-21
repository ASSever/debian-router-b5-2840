package network

import (
	"errors"
	"testing"
)

func TestReservationChanges(t *testing.T) {
	entry := DHCPReservation{MAC: "02:0A:00:00:00:01", Address: "192.168.10.20"}
	entries, err := updateReservations(defaultLAN, nil, ReservationChange{Reservation: &entry})
	if err != nil || len(entries) != 1 || entries[0].MAC != "02:0a:00:00:00:01" {
		t.Fatalf("create = %+v, %v", entries, err)
	}
	duplicate := DHCPReservation{MAC: "02:0a:00:00:00:02", Address: entry.Address}
	if _, err := updateReservations(defaultLAN, entries, ReservationChange{Reservation: &duplicate}); !errors.Is(err, ErrReservationConflict) {
		t.Fatalf("duplicate address error = %v", err)
	}
	for _, address := range []string{"192.168.10.0", "192.168.10.1", "192.168.10.255", "192.168.1.20"} {
		if validReservationAddress(defaultLAN, address) {
			t.Errorf("accepted reserved address %s", address)
		}
	}
	entries, err = updateReservations(defaultLAN, entries, ReservationChange{PreviousMAC: entry.MAC, Reservation: &duplicate})
	if err != nil || len(entries) != 1 || entries[0] != duplicate {
		t.Fatalf("edit = %+v, %v", entries, err)
	}
	entries, err = updateReservations(defaultLAN, entries, ReservationChange{PreviousMAC: duplicate.MAC})
	if err != nil || len(entries) != 0 {
		t.Fatalf("delete = %+v, %v", entries, err)
	}
}
