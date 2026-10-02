package config

import "errors"

// Core is the core service configuration.
type Core struct {
	Common
	PaymentsGRPCAddr    string
	ProvisionerGRPCAddr string
	// MasterKey encrypts TOTP secrets at rest (envelope).
	MasterKey string
}

// LoadCore reads the core service configuration, failing closed on missing
// peer addresses or encryption material.
func LoadCore() (Core, error) {
	var errs []error
	c, err := Load("core")
	if err != nil {
		errs = append(errs, err)
	}
	co := Core{Common: c}
	co.PaymentsGRPCAddr = getenv("BOBRES_PAYMENTS_GRPC_ADDR", "")
	if co.PaymentsGRPCAddr == "" {
		errs = append(errs, errors.New("BOBRES_PAYMENTS_GRPC_ADDR is required"))
	}
	co.ProvisionerGRPCAddr = getenv("BOBRES_PROVISIONER_GRPC_ADDR", "")
	if co.ProvisionerGRPCAddr == "" {
		errs = append(errs, errors.New("BOBRES_PROVISIONER_GRPC_ADDR is required"))
	}
	if v, err := Secret("BOBRES_MASTER_KEY"); err != nil || len(v) < 32 {
		errs = append(errs, err)
		errs = append(errs, errors.New("BOBRES_MASTER_KEY must be at least 32 characters"))
	} else {
		co.MasterKey = v
	}
	return co, errors.Join(errs...)
}

// Payments is the payments service configuration. Phase 1 needs no extra
// fields beyond Common, but the named type keeps parity with other services.
type Payments struct {
	Common
}

// LoadPayments reads the payments service configuration.
func LoadPayments() (Payments, error) {
	c, err := Load("payments")
	return Payments{Common: c}, err
}

// Provisioner is the provisioner service configuration.
type Provisioner struct {
	Common
	// MasterKey encrypts 3x-ui API tokens at rest (envelope).
	MasterKey string
}

// LoadProvisioner reads the provisioner service configuration.
func LoadProvisioner() (Provisioner, error) {
	var errs []error
	c, err := Load("provisioner")
	if err != nil {
		errs = append(errs, err)
	}
	p := Provisioner{Common: c}
	if v, err := Secret("BOBRES_MASTER_KEY"); err != nil || len(v) < 32 {
		errs = append(errs, err)
		errs = append(errs, errors.New("BOBRES_MASTER_KEY must be at least 32 characters"))
	} else {
		p.MasterKey = v
	}
	return p, errors.Join(errs...)
}
