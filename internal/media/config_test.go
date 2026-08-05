package media

import (
	"testing"

	"github.com/i5heu/MentisEterna/internal/config"
)

func TestBuildEndpointsFailsWithoutConfigDefs(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	if _, err := BuildEndpoints(); err == nil {
		t.Fatal("expected BuildEndpoints to fail with no media.endpoints")
	}
}

func TestBuildEndpointsCombinesConfigAndEnvKeys(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().Media.Endpoints = []config.MediaEndpointConfig{
		{ID: "primary", Bucket: "b1", Region: "eu", Endpoint: "https://s3.example", ForcePathStyle: true},
	}
	t.Setenv("MEDIA_S3_PRIMARY_ACCESS_KEY_ID", "ak")
	t.Setenv("MEDIA_S3_PRIMARY_SECRET_ACCESS_KEY", "sk")

	eps, err := BuildEndpoints()
	if err != nil {
		t.Fatalf("BuildEndpoints: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(eps))
	}
	ep := eps[0]
	if ep.ID != "primary" || ep.Bucket != "b1" || ep.Endpoint != "https://s3.example" {
		t.Fatalf("endpoint definition not carried through: %+v", ep)
	}
	if ep.AccessKeyID != "ak" || ep.SecretAccessKey != "sk" {
		t.Fatalf("keys not resolved from env: %+v", ep)
	}
	if !ep.ForcePathStyle {
		t.Fatal("expected force_path_style to be carried through")
	}
}

func TestBuildEndpointsRequiresKeys(t *testing.T) {
	config.Reset()
	t.Cleanup(config.Reset)
	config.Get().Media.Endpoints = []config.MediaEndpointConfig{
		{ID: "primary", Bucket: "b1", Endpoint: "https://s3.example"},
	}
	if _, err := BuildEndpoints(); err == nil {
		t.Fatal("expected BuildEndpoints to fail without MEDIA_S3_PRIMARY_ACCESS_KEY_ID")
	}
}
