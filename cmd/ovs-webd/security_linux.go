//go:build linux

package main

import (
	"context"
	"crypto/x509"
	"errors"
	"github.com/sampsonlor/ovs-webui/internal/authn"
	"github.com/sampsonlor/ovs-webui/internal/repository/certificates"
	"github.com/sampsonlor/ovs-webui/internal/repository/secrets"
	"github.com/sampsonlor/ovs-webui/internal/repository/sessions"
	"github.com/sampsonlor/ovs-webui/internal/repository/sqlite"
	"github.com/sampsonlor/ovs-webui/internal/secret"
	"net/url"
	"os"
	"path/filepath"
)

type securityOptions struct{ origin, tlsDir, sessionDir, legacySession, roots string }

func (o securityOptions) sessionKeys(importLegacy bool) (secret.Ring, error) {
	if _, err := os.Lstat(o.sessionDir); err == nil {
		return secret.LoadKeys(o.sessionDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return secret.Ring{}, secret.ErrUnavailable
	}
	key, err := authn.KeyFile(o.legacySession, false)
	if err != nil {
		return secret.Ring{}, err
	}
	if importLegacy {
		return secret.InitializeKeys(o.sessionDir, key)
	}
	return secret.NewRing(1, map[int][]byte{1: key})
}
func (o securityOptions) certificates(ctx context.Context, db *sqlite.Store, initialize bool) (*certificates.Store, error) {
	origin, err := url.Parse(o.origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" {
		return nil, secret.ErrUnavailable
	}
	var roots *x509.CertPool
	if o.roots != "" {
		data, err := secret.ReadPrivateFile(o.roots, 256<<10)
		if err != nil {
			return nil, err
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return nil, secret.ErrUnavailable
		}
	}
	var ring secret.Ring
	if initialize {
		if err = certificates.CheckUninitialized(ctx, db); err != nil {
			return nil, err
		}
		ring, err = secret.InitializeKeys(o.tlsDir, nil)
	} else {
		ring, err = secret.LoadKeys(o.tlsDir)
	}
	if err != nil {
		return nil, err
	}
	partition, err := secrets.New(ctx, db, secret.TLS, ring)
	if err != nil {
		return nil, err
	}
	return certificates.New(partition, origin.Hostname(), roots)
}
func (o securityOptions) offline(ctx context.Context, db *sqlite.Store, bootstrap, printCert, restore bool, rotate string) error {
	if rotate == "session" {
		ring, err := o.sessionKeys(true)
		if err != nil {
			return err
		}
		repo, err := sessions.NewWithKeys(ctx, db, ring)
		if err != nil {
			return err
		}
		next, err := secret.PrepareKey(o.sessionDir)
		if err != nil {
			return err
		}
		if err = repo.Rewrap(ctx, next); err != nil {
			return err
		}
		return secret.ActivateKeys(o.sessionDir, next.Active)
	}
	if restore {
		ring, err := o.sessionKeys(false)
		if err != nil {
			return err
		}
		repo, err := sessions.NewWithKeys(ctx, db, ring)
		if err != nil {
			return err
		}
		if _, err = os.Lstat(o.tlsDir); err == nil {
			certs, err := o.certificates(ctx, db, false)
			if err != nil {
				return err
			}
			if err = certs.PrepareRestore(ctx); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return repo.PrepareRestore(ctx)
	}
	certs, err := o.certificates(ctx, db, bootstrap)
	if err != nil {
		return err
	}
	if bootstrap {
		return certs.Initialize(ctx)
	}
	if printCert {
		data, err := certs.PublicCertificate(ctx)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	}
	if rotate != "tls" {
		return secret.ErrUnavailable
	}
	next, err := secret.PrepareKey(o.tlsDir)
	if err != nil {
		return err
	}
	if err = certs.Rewrap(ctx, next); err != nil {
		return err
	}
	return secret.ActivateKeys(o.tlsDir, next.Active)
}
func defaultDirectory(database, name string) string {
	return filepath.Join(filepath.Dir(database), name)
}
