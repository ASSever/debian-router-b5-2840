package network

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const maxReservations = 128

type DHCPReservation struct {
	MAC     string `json:"mac"`
	Address string `json:"address"`
}

type ReservationChange struct {
	PreviousMAC string           `json:"previous_mac,omitempty"`
	Reservation *DHCPReservation `json:"reservation,omitempty"`
}

type DHCPReservations struct {
	Managed bool              `json:"managed"`
	Entries []DHCPReservation `json:"entries"`
}

var (
	ErrReservationInvalid      = errors.New("invalid_reservation")
	ErrReservationConflict     = errors.New("reservation_conflict")
	ErrReservationMissing      = errors.New("reservation_missing")
	ErrReservationLimit        = errors.New("reservation_limit")
	ErrReservationsUnavailable = errors.New("reservations_unavailable")
)

func normalizeReservationMAC(value string) string {
	if !validWANMAC(value) {
		return ""
	}
	mac, _ := net.ParseMAC(value)
	return mac.String()
}

func validReservationAddress(config LANConfig, value string) bool {
	prefix := netip.MustParsePrefix(config.Address)
	address, err := netip.ParseAddr(value)
	return err == nil && address != prefix.Addr() && usableLANAddress(prefix.Masked(), address)
}

func (m *StaticWANManager) reservationPath() string {
	return filepath.Join(filepath.Dir(m.configPath), "20-router-dhcp.hosts")
}

func (m *StaticWANManager) readReservations() ([]byte, []DHCPReservation, error) {
	data, _, _, err := readStaticWANFile(m.reservationPath())
	entries := []DHCPReservation{}
	if err != nil || len(data) == 0 {
		return data, entries, err
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		mac, address, ok := strings.Cut(line, ",")
		ip, err := netip.ParseAddr(address)
		if !ok || mac == "" || normalizeReservationMAC(mac) != mac || err != nil || !ip.Is4() || len(entries) >= maxReservations {
			return nil, nil, errors.New("invalid DHCP reservation file")
		}
		entries = append(entries, DHCPReservation{MAC: mac, Address: address})
	}
	return data, entries, nil
}

func (m *StaticWANManager) ListDHCPReservations() (DHCPReservations, error) {
	var result DHCPReservations
	err := withFileLock(wanLockPath(m.statePath), func() error {
		config, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		result.Managed = config != nil
		_, result.Entries, err = m.readReservations()
		return err
	})
	return result, err
}

func updateReservations(config LANConfig, entries []DHCPReservation, change ReservationChange) ([]DHCPReservation, error) {
	if change.PreviousMAC != "" {
		old := normalizeReservationMAC(change.PreviousMAC)
		if old == "" {
			return nil, ErrReservationInvalid
		}
		index := slices.IndexFunc(entries, func(entry DHCPReservation) bool { return entry.MAC == old })
		if index < 0 {
			return nil, ErrReservationMissing
		}
		entries = slices.Delete(entries, index, index+1)
	}
	if change.Reservation == nil {
		if change.PreviousMAC == "" {
			return nil, ErrReservationInvalid
		}
		return entries, nil
	}
	entry := *change.Reservation
	entry.MAC = normalizeReservationMAC(entry.MAC)
	if entry.MAC == "" || !validReservationAddress(config, entry.Address) {
		return nil, ErrReservationInvalid
	}
	for _, other := range entries {
		if entry.MAC == other.MAC || entry.Address == other.Address {
			return nil, ErrReservationConflict
		}
	}
	if len(entries) >= maxReservations {
		return nil, ErrReservationLimit
	}
	entries = append(entries, entry)
	slices.SortFunc(entries, func(a, b DHCPReservation) int { return strings.Compare(a.MAC, b.MAC) })
	return entries, nil
}

func (m *StaticWANManager) ChangeDHCPReservation(ctx context.Context, change ReservationChange) error {
	return withFileLock(wanLockPath(m.statePath), func() error {
		pending, err := m.hasPendingTransaction()
		if err != nil {
			return err
		}
		if pending {
			return ErrStaticWANPending
		}
		config, err := ManagedLAN(m.lanPath())
		if err != nil {
			return err
		}
		if config == nil {
			return ErrReservationsUnavailable
		}
		if _, err := m.run(ctx, "/usr/bin/systemctl", "--system", "is-active", lanDNSUnit); err != nil {
			return ErrReservationsUnavailable
		}
		previous, entries, err := m.readReservations()
		if err != nil {
			return err
		}
		entries, err = updateReservations(*config, entries, change)
		if err != nil {
			return err
		}
		var content strings.Builder
		for _, entry := range entries {
			content.WriteString(entry.MAC + "," + entry.Address + "\n")
		}
		path := m.reservationPath()
		if err := writeAtomicFile(path, []byte(content.String()), 0644); err != nil {
			return err
		}
		if err := m.reloadLANDNS(ctx); err != nil {
			if restoreErr := writeAtomicFile(path, previous, 0644); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
			restoreCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			return errors.Join(err, m.reloadLANDNS(restoreCtx))
		}
		return nil
	})
}
