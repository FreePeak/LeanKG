package main

import "errors"

// hostedOpts are the serve flags that --hosted must agree with.
type hostedOpts struct {
	readOnly     bool
	memoryGlobal bool
	stdio        bool
	rest         string
	rpc          string
	ui           string
}

// checkHostedFlags refuses a flag combination a hosted control plane cannot
// serve. It runs before any store is opened, so a refused serve leaves no
// file behind. The home store must be writable (it holds the tokens, the org
// claims and the registry); the dashboard, ConnectRPC and stdio surfaces are
// off, and the project admin API lives on the REST listener.
func checkHostedFlags(hosted bool, o hostedOpts) error {
	if !hosted {
		return nil
	}
	switch {
	case o.readOnly:
		return errors.New("--hosted needs a writable home store (it holds tokens, org claims and the registry): drop --read-only; routed project stores are read-only on their own")
	case o.memoryGlobal:
		return errors.New("--hosted refuses --memory-global: the memory root is per project")
	case o.ui != "":
		return errors.New("--hosted refuses --ui: the dashboard API is unauthenticated")
	case o.rpc != "":
		return errors.New("--hosted refuses --rpc: serve the MCP and REST listeners only")
	case o.stdio:
		return errors.New("--hosted refuses --stdio: a hosted server is a network listener")
	case o.rest == "":
		return errors.New("--hosted needs --rest: the project admin API is served on the REST listener")
	}
	return nil
}

// hostedGateError is the refusal for --hosted with no token source: a hosted
// server with an open gate would answer every caller as Admin.
func hostedGateError() error {
	return errors.New("--hosted needs a token source: set LEANKG_TOKEN_ADMIN (or _CONTRIBUTOR/_VIEWER), mint one with `leankg auth token create`, or create a site admin with `leankg auth admin init`")
}
