package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/privilegedagent"
	"github.com/ASSever/debian-router-b5-2840/backend/internal/proxy"
)

func (a *api) proxyProfile(w http.ResponseWriter, r *http.Request) {
	request := privilegedagent.Request{Operation: "proxy-profile-status"}
	if r.Method == http.MethodPut {
		if !a.authorizeNetworkMutation(w, r) {
			return
		}
		if !a.proxyImportLock.TryLock() {
			proxyImportError(w, http.StatusConflict, "subscription_busy")
			return
		}
		defer a.proxyImportLock.Unlock()
		var profile proxy.Profile
		if err := decodeJSON(w, r, &profile); err != nil {
			proxyImportError(w, http.StatusBadRequest, "invalid_proxy_profile")
			return
		}
		change, err := proxy.PrepareProfile(proxyStateDirectory(), profile)
		if err != nil {
			proxyProfileError(w, err)
			return
		}
		request.Operation = "proxy-profile-apply"
		request.ProxyProfile = &change
	} else if !a.authorizeAdministrator(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	response, err := privilegedagent.SendLAN(ctx, agentSocketPath(), request)
	if err != nil {
		proxyProfileError(w, proxy.ErrProfileUnavailable)
		return
	}
	if response.Error != "" || response.ProxyProfile == nil {
		proxyProfileError(w, errors.New(response.Error))
		return
	}
	if r.Method == http.MethodPut {
		log.Print("proxy profile applied")
	}
	writeJSON(w, http.StatusOK, response.ProxyProfile)
}

func proxyProfileError(w http.ResponseWriter, err error) {
	code := err.Error()
	switch code {
	case "invalid_proxy_profile", "server_not_found", "proxy_config_rejected":
		proxyImportError(w, http.StatusBadRequest, code)
	case "subscription_changed":
		proxyImportError(w, http.StatusConflict, code)
	case "proxy_apply_rolled_back", "proxy_rollback_failed":
		proxyImportError(w, http.StatusServiceUnavailable, code)
	default:
		proxyImportError(w, http.StatusServiceUnavailable, "proxy_profile_unavailable")
	}
}
