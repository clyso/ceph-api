package rados

import (
	"errors"
	"testing"

	cephrados "github.com/ceph/go-ceph/rados"
)

func TestIsConfigKeyGetNotFound(t *testing.T) {
	if !isConfigKeyGetNotFound(`{"prefix":"config-key get","key":"ceph-api/auth/apikeys/ak_test"}`, cephrados.ErrNotFound) {
		t.Fatal("config-key get not found was not recognized")
	}
	if isConfigKeyGetNotFound(`{"prefix":"config-key set","key":"ceph-api/auth/apikeys/ak_test"}`, cephrados.ErrNotFound) {
		t.Fatal("config-key set not found was recognized as expected get miss")
	}
	if isConfigKeyGetNotFound(`{"prefix":"config-key get","key":"ceph-api/auth/apikeys/ak_test"}`, errors.New("boom")) {
		t.Fatal("non-not-found error was recognized as expected get miss")
	}
}
