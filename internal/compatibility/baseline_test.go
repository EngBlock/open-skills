package compatibility

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type baselineRoundTripFunc func(*http.Request) (*http.Response, error)

func (function baselineRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type baselineFixture struct {
	manifest  BaselineManifest
	responses map[string]any
	raw       map[string][]byte
	artifact  []byte
	status    map[string]int
}

func newBaselineFixture(t *testing.T) *baselineFixture {
	t.Helper()
	manifest, err := ReadBaselineManifest("../../compatibility/npm-0.1.2/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("frozen npm oracle")
	one, two, five := sha1.Sum(artifact), sha256.Sum256(artifact), sha512.Sum512(artifact)
	manifest.Package.MetadataURL = "https://registry.test/version"
	manifest.Package.PublicationMetadataURL = "https://registry.test/package"
	manifest.Artifact.URL = "https://registry.test/artifact.tgz"
	manifest.Artifact.SHA1 = hex.EncodeToString(one[:])
	manifest.Artifact.SHA256 = hex.EncodeToString(two[:])
	manifest.Artifact.SHA512 = hex.EncodeToString(five[:])
	manifest.Artifact.Integrity = "sha512-" + base64.StdEncoding.EncodeToString(five[:])
	manifest.Artifact.Size = int64(len(artifact))
	manifest.Source.TagObjectURL = "https://api.github.com/repos/EngBlock/open-skills/git/tags/test-tag-object"
	manifest.Source.CommitURL = "https://api.github.com/repos/EngBlock/open-skills/git/commits/test-commit"
	manifest.Source.Protection.RulesetURL = "https://api.github.com/repos/EngBlock/open-skills/rulesets/test-ruleset"

	responses := map[string]any{
		manifest.Package.MetadataURL: map[string]any{
			"name": manifest.Package.Name, "version": manifest.Package.Version,
			"repository": map[string]any{"url": "git+https://github.com/EngBlock/open-skills.git"},
			"dist": map[string]any{
				"tarball": manifest.Artifact.URL, "integrity": manifest.Artifact.Integrity,
				"shasum": manifest.Artifact.SHA1, "fileCount": manifest.Artifact.FileCount,
				"unpackedSize": manifest.Artifact.UnpackedSize,
				"signatures": []any{map[string]any{
					"keyid": manifest.Artifact.NPMSignature.KeyID,
					"sig":   manifest.Artifact.NPMSignature.Signature,
				}},
			},
		},
		manifest.Package.PublicationMetadataURL: map[string]any{
			"time": map[string]any{manifest.Package.Version: manifest.Package.PublishedAt},
		},
		manifest.Source.TagRefURL: map[string]any{
			"object": map[string]any{"type": "tag", "sha": manifest.Source.TagObject},
		},
		manifest.Source.TagObjectURL: map[string]any{
			"sha": manifest.Source.TagObject, "tag": manifest.Source.Tag,
			"object":       map[string]any{"type": "commit", "sha": manifest.Source.Commit},
			"verification": map[string]any{"verified": manifest.Source.TagSigned},
		},
		manifest.Source.CommitURL: map[string]any{
			"sha": manifest.Source.Commit, "tree": map[string]any{"sha": manifest.Source.Tree},
		},
		manifest.Source.Protection.RulesetURL: map[string]any{
			"id": manifest.Source.Protection.RulesetID, "name": manifest.Source.Protection.RulesetName,
			"target": "tag", "enforcement": "active",
			"conditions": map[string]any{"ref_name": map[string]any{
				"include": []any{"refs/tags/" + manifest.Source.Tag}, "exclude": []any{},
			}},
			"rules":                   []any{map[string]any{"type": "update"}, map[string]any{"type": "deletion"}},
			"bypass_actors":           []any{},
			"current_user_can_bypass": "never",
		},
	}
	return &baselineFixture{
		manifest: manifest, responses: responses, raw: map[string][]byte{}, artifact: artifact, status: map[string]int{},
	}
}

func (fixture *baselineFixture) client(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: baselineRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		status := fixture.status[request.URL.String()]
		if status == 0 {
			status = http.StatusOK
		}
		var data []byte
		if request.URL.String() == fixture.manifest.Artifact.URL {
			data = fixture.artifact
		} else if raw, exists := fixture.raw[request.URL.String()]; exists {
			data = raw
		} else if body, exists := fixture.responses[request.URL.String()]; exists {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			status = http.StatusNotFound
			data = []byte(`{"error":"not found"}`)
		}
		return &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       io.NopCloser(bytes.NewReader(data)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}
}

func TestBaselineManifestPinsNPMArtifactAndSourceIdentities(t *testing.T) {
	manifest, err := ReadBaselineManifest("../../compatibility/npm-0.1.2/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.Package.Name != "@engblock/open-skills" || manifest.Package.Version != "0.1.2" || manifest.Package.PublishedAt != "2026-07-22T14:13:45.715Z" {
		t.Fatalf("unexpected package identity: %#v", manifest.Package)
	}
	if manifest.Artifact.Integrity != "sha512-TTD/WemKLYiem5bM+vEtuxXORSZExvP4wdDxCELP1xpSqlSnEzMa2xe61JO7xBMFEbSrtBqEyys+cHVEDyCMFg==" || manifest.Artifact.SHA1 != "159fb3c760ea72b731674c6a93059a37040a0c1f" || manifest.Artifact.SHA256 != "2871993290bb28ae40d3a1c59f64b1e29564de4145a62bd3c9bdf7b85aef39c9" {
		t.Fatalf("unexpected artifact identity: %#v", manifest.Artifact)
	}
	if manifest.Source.Repository != "https://github.com/EngBlock/open-skills" || manifest.Source.Tag != "v0.1.2" || manifest.Source.TagObject != "b3117c12d841b5fdfc3c2fead72c39d01e148ab2" || manifest.Source.Commit != "a91eb79d035d7a33300d2cc60b18db3f81a94621" || manifest.Source.Tree != "f766eaf80048c8f5232eaa981bfd1fa45485fc70" || manifest.Source.Protection.RulesetID != 19578936 || manifest.Source.Protection.RulesetName != "Protect npm 0.1.2 compatibility baseline" {
		t.Fatalf("unexpected source identity: %#v", manifest.Source)
	}
}

func TestVerifyBaselineChecksRegistryArtifactTagChainAndProtection(t *testing.T) {
	fixture := newBaselineFixture(t)
	if err := VerifyBaseline(context.Background(), fixture.manifest, fixture.client(t), "token"); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyBaselineFailsClosedOnDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*baselineFixture)
		want   string
	}{
		{name: "registry metadata", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Package.MetadataURL].(map[string]any)["dist"].(map[string]any)["integrity"] = "sha512-drifted"
		}, want: "artifact.integrity mismatch"},
		{name: "source repository", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Package.MetadataURL].(map[string]any)["repository"].(map[string]any)["url"] = "git+https://github.com/other/repository.git"
		}, want: "source.repository mismatch"},
		{name: "tag ref", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.TagRefURL].(map[string]any)["object"].(map[string]any)["sha"] = "drifted"
		}, want: "source tag ref mismatch"},
		{name: "tag target", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.TagObjectURL].(map[string]any)["object"].(map[string]any)["sha"] = "drifted"
		}, want: "source tag target mismatch"},
		{name: "missing tag signature status", mutate: func(f *baselineFixture) {
			delete(f.responses[f.manifest.Source.TagObjectURL].(map[string]any)["verification"].(map[string]any), "verified")
		}, want: "source tag signature status mismatch"},
		{name: "null tag signature status", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.TagObjectURL].(map[string]any)["verification"].(map[string]any)["verified"] = nil
		}, want: "source tag signature status mismatch"},
		{name: "commit", mutate: func(f *baselineFixture) { f.responses[f.manifest.Source.CommitURL].(map[string]any)["sha"] = "drifted" }, want: "source commit mismatch"},
		{name: "tree", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.CommitURL].(map[string]any)["tree"].(map[string]any)["sha"] = "drifted"
		}, want: "source tree mismatch"},
		{name: "rules", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.Protection.RulesetURL].(map[string]any)["rules"] = []any{map[string]any{"type": "update"}}
		}, want: "source protection rules mismatch: missing deletion"},
		{name: "exclusions", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.Protection.RulesetURL].(map[string]any)["conditions"].(map[string]any)["ref_name"].(map[string]any)["exclude"] = []any{"refs/tags/v0.1.2"}
		}, want: "source protection exclude mismatch"},
		{name: "missing bypass actors", mutate: func(f *baselineFixture) {
			delete(f.responses[f.manifest.Source.Protection.RulesetURL].(map[string]any), "bypass_actors")
		}, want: "source protection bypass actors mismatch"},
		{name: "bypass permission", mutate: func(f *baselineFixture) {
			f.responses[f.manifest.Source.Protection.RulesetURL].(map[string]any)["current_user_can_bypass"] = "always"
		}, want: "source protection current-user bypass mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBaselineFixture(t)
			test.mutate(fixture)
			err := VerifyBaseline(context.Background(), fixture.manifest, fixture.client(t), "token")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestVerifyBaselineFailsClosedWhenResourceIsUnavailable(t *testing.T) {
	fixture := newBaselineFixture(t)
	fixture.status[fixture.manifest.Source.TagRefURL] = http.StatusServiceUnavailable
	err := VerifyBaseline(context.Background(), fixture.manifest, fixture.client(t), "token")
	if err == nil || !strings.Contains(err.Error(), "GET "+fixture.manifest.Source.TagRefURL+" failed: Service Unavailable") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyBaselineFailsClosedWhenArtifactBytesDiffer(t *testing.T) {
	fixture := newBaselineFixture(t)
	fixture.artifact = append([]byte(nil), fixture.artifact...)
	fixture.artifact[0] ^= 1
	err := VerifyBaseline(context.Background(), fixture.manifest, fixture.client(t), "token")
	if err == nil || !strings.Contains(err.Error(), "artifact.sha1 bytes mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyBaselineFailsClosedOnTrailingJSON(t *testing.T) {
	for _, suffix := range []string{"{}", " trailing"} {
		fixture := newBaselineFixture(t)
		encoded, err := json.Marshal(fixture.responses[fixture.manifest.Source.TagRefURL])
		if err != nil {
			t.Fatal(err)
		}
		fixture.raw[fixture.manifest.Source.TagRefURL] = append(encoded, suffix...)
		err = VerifyBaseline(context.Background(), fixture.manifest, fixture.client(t), "token")
		if err == nil || !strings.Contains(err.Error(), "decode GET "+fixture.manifest.Source.TagRefURL) {
			t.Fatalf("suffix %q: error = %v", suffix, err)
		}
	}
}

func TestResolveGitHubTokenPrecedenceAndFallback(t *testing.T) {
	for _, test := range []struct {
		name    string
		values  map[string]string
		gh      []byte
		ghErr   error
		want    string
		wantErr bool
	}{
		{name: "GitHub token", values: map[string]string{"GITHUB_TOKEN": "github", "GH_TOKEN": "gh"}, ghErr: errors.New("must not run"), want: "github"},
		{name: "gh token", values: map[string]string{"GH_TOKEN": "gh"}, ghErr: errors.New("must not run"), want: "gh"},
		{name: "gh auth fallback", values: map[string]string{}, gh: []byte("cli-token\n"), want: "cli-token"},
		{name: "missing authentication", values: map[string]string{}, ghErr: errors.New("not logged in"), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			token, err := resolveGitHubToken(func(name string) string { return test.values[name] }, func() ([]byte, error) {
				called = true
				return test.gh, test.ghErr
			})
			if test.wantErr {
				if err == nil {
					t.Fatal("expected authentication error")
				}
				return
			}
			if err != nil || token != test.want {
				t.Fatalf("token = %q, error = %v; want %q", token, err, test.want)
			}
			if test.want != "cli-token" && called {
				t.Fatal("gh fallback ran despite an environment token")
			}
		})
	}
}
