package compatibility

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// BaselineManifest records the immutable npm artifact and source identities
// used for historical compatibility inspection.
type BaselineManifest struct {
	SchemaVersion int `json:"schemaVersion"`
	Package       struct {
		Name                   string `json:"name"`
		Version                string `json:"version"`
		PublishedAt            string `json:"publishedAt"`
		MetadataURL            string `json:"metadataUrl"`
		PublicationMetadataURL string `json:"publicationMetadataUrl"`
	} `json:"package"`
	Artifact struct {
		URL          string `json:"url"`
		Integrity    string `json:"integrity"`
		SHA1         string `json:"sha1"`
		SHA256       string `json:"sha256"`
		SHA512       string `json:"sha512"`
		Size         int64  `json:"size"`
		FileCount    int    `json:"fileCount"`
		UnpackedSize int64  `json:"unpackedSize"`
		NPMSignature struct {
			KeyID     string `json:"keyId"`
			Signature string `json:"signature"`
		} `json:"npmSignature"`
	} `json:"artifact"`
	Source struct {
		Repository   string `json:"repository"`
		Tag          string `json:"tag"`
		TagRefURL    string `json:"tagRefUrl"`
		TagObject    string `json:"tagObject"`
		TagObjectURL string `json:"tagObjectUrl"`
		Commit       string `json:"commit"`
		CommitURL    string `json:"commitUrl"`
		Tree         string `json:"tree"`
		TagSigned    bool   `json:"tagSigned"`
		Protection   struct {
			RulesetID   int64  `json:"rulesetId"`
			RulesetName string `json:"rulesetName"`
			RulesetURL  string `json:"rulesetUrl"`
		} `json:"protection"`
	} `json:"source"`
}

// ReadBaselineManifest decodes the reviewed compatibility manifest.
func ReadBaselineManifest(path string) (BaselineManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BaselineManifest{}, err
	}
	var manifest BaselineManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return BaselineManifest{}, fmt.Errorf("decode baseline manifest: %w", err)
	}
	return manifest, nil
}

// VerifyBaseline checks the live registry artifact, annotated source tag chain,
// and tag protection against the immutable manifest. It never resolves a
// package dist-tag or executes npm.
func VerifyBaseline(ctx context.Context, manifest BaselineManifest, client *http.Client, githubToken string) error {
	if manifest.SchemaVersion != 1 {
		return mismatch("schemaVersion", manifest.SchemaVersion, 1)
	}
	if client == nil {
		client = http.DefaultClient
	}

	var metadata baselinePackageMetadata
	if err := getBaselineJSON(ctx, client, manifest.Package.MetadataURL, "", &metadata); err != nil {
		return err
	}
	var publication baselinePublicationMetadata
	if err := getBaselineJSON(ctx, client, manifest.Package.PublicationMetadataURL, "", &publication); err != nil {
		return err
	}
	var tagRef baselineGitObject
	if err := getBaselineJSON(ctx, client, manifest.Source.TagRefURL, githubToken, &tagRef); err != nil {
		return err
	}
	var tagObject baselineTagObject
	if err := getBaselineJSON(ctx, client, manifest.Source.TagObjectURL, githubToken, &tagObject); err != nil {
		return err
	}
	var commit baselineCommit
	if err := getBaselineJSON(ctx, client, manifest.Source.CommitURL, githubToken, &commit); err != nil {
		return err
	}
	var ruleset baselineRuleset
	if err := getBaselineJSON(ctx, client, manifest.Source.Protection.RulesetURL, githubToken, &ruleset); err != nil {
		return err
	}
	artifact, err := getBaselineBytes(ctx, client, manifest.Artifact.URL)
	if err != nil {
		return err
	}

	if metadata.Name != manifest.Package.Name {
		return mismatch("package.name", metadata.Name, manifest.Package.Name)
	}
	if metadata.Version != manifest.Package.Version {
		return mismatch("package.version", metadata.Version, manifest.Package.Version)
	}
	repository := strings.TrimSuffix(strings.TrimPrefix(metadata.Repository.URL, "git+"), ".git")
	if repository != manifest.Source.Repository {
		return mismatch("source.repository", repository, manifest.Source.Repository)
	}
	parsedRepository, err := url.Parse(manifest.Source.Repository)
	if err != nil || parsedRepository.Scheme != "https" || parsedRepository.Host != "github.com" {
		return mismatch("source.repository host", parsedRepositoryOrigin(parsedRepository), "https://github.com")
	}
	repositoryPath := strings.Trim(parsedRepository.Path, "/")
	apiPrefix := "https://api.github.com/repos/" + repositoryPath + "/"
	for field, value := range map[string]string{
		"source.tagRefUrl":             manifest.Source.TagRefURL,
		"source.tagObjectUrl":          manifest.Source.TagObjectURL,
		"source.commitUrl":             manifest.Source.CommitURL,
		"source.protection.rulesetUrl": manifest.Source.Protection.RulesetURL,
	} {
		if !strings.HasPrefix(value, apiPrefix) {
			return fmt.Errorf("%s mismatch: expected URL under %s, got %s", field, apiPrefix, value)
		}
	}
	if publication.Time[manifest.Package.Version] != manifest.Package.PublishedAt {
		return mismatch("package.publishedAt", publication.Time[manifest.Package.Version], manifest.Package.PublishedAt)
	}
	if metadata.Dist.Tarball != manifest.Artifact.URL {
		return mismatch("artifact.url", metadata.Dist.Tarball, manifest.Artifact.URL)
	}
	if metadata.Dist.Integrity != manifest.Artifact.Integrity {
		return mismatch("artifact.integrity", metadata.Dist.Integrity, manifest.Artifact.Integrity)
	}
	if metadata.Dist.Shasum != manifest.Artifact.SHA1 {
		return mismatch("artifact.sha1 metadata", metadata.Dist.Shasum, manifest.Artifact.SHA1)
	}
	if metadata.Dist.FileCount != manifest.Artifact.FileCount {
		return mismatch("artifact.fileCount", metadata.Dist.FileCount, manifest.Artifact.FileCount)
	}
	if metadata.Dist.UnpackedSize != manifest.Artifact.UnpackedSize {
		return mismatch("artifact.unpackedSize", metadata.Dist.UnpackedSize, manifest.Artifact.UnpackedSize)
	}
	if len(metadata.Dist.Signatures) == 0 {
		return mismatch("artifact.npmSignature.keyId", "", manifest.Artifact.NPMSignature.KeyID)
	}
	if metadata.Dist.Signatures[0].KeyID != manifest.Artifact.NPMSignature.KeyID {
		return mismatch("artifact.npmSignature.keyId", metadata.Dist.Signatures[0].KeyID, manifest.Artifact.NPMSignature.KeyID)
	}
	if metadata.Dist.Signatures[0].Signature != manifest.Artifact.NPMSignature.Signature {
		return mismatch("artifact.npmSignature.signature", metadata.Dist.Signatures[0].Signature, manifest.Artifact.NPMSignature.Signature)
	}

	one, two, five := sha1.Sum(artifact), sha256.Sum256(artifact), sha512.Sum512(artifact)
	if int64(len(artifact)) != manifest.Artifact.Size {
		return mismatch("artifact.size", len(artifact), manifest.Artifact.Size)
	}
	if actual := hex.EncodeToString(one[:]); actual != manifest.Artifact.SHA1 {
		return mismatch("artifact.sha1 bytes", actual, manifest.Artifact.SHA1)
	}
	if actual := hex.EncodeToString(two[:]); actual != manifest.Artifact.SHA256 {
		return mismatch("artifact.sha256 bytes", actual, manifest.Artifact.SHA256)
	}
	if actual := hex.EncodeToString(five[:]); actual != manifest.Artifact.SHA512 {
		return mismatch("artifact.sha512 bytes", actual, manifest.Artifact.SHA512)
	}
	if actual := "sha512-" + base64.StdEncoding.EncodeToString(five[:]); actual != manifest.Artifact.Integrity {
		return mismatch("artifact SRI", actual, manifest.Artifact.Integrity)
	}

	if tagRef.Object.Type != "tag" {
		return mismatch("source tag ref type", tagRef.Object.Type, "tag")
	}
	if tagRef.Object.SHA != manifest.Source.TagObject {
		return mismatch("source tag ref", tagRef.Object.SHA, manifest.Source.TagObject)
	}
	if tagObject.SHA != manifest.Source.TagObject {
		return mismatch("source tag object", tagObject.SHA, manifest.Source.TagObject)
	}
	if tagObject.Tag != manifest.Source.Tag {
		return mismatch("source tag name", tagObject.Tag, manifest.Source.Tag)
	}
	if tagObject.Object.Type != "commit" {
		return mismatch("source tag target type", tagObject.Object.Type, "commit")
	}
	if tagObject.Object.SHA != manifest.Source.Commit {
		return mismatch("source tag target", tagObject.Object.SHA, manifest.Source.Commit)
	}
	if tagObject.Verification.Verified == nil {
		return mismatch("source tag signature status", nil, manifest.Source.TagSigned)
	}
	if *tagObject.Verification.Verified != manifest.Source.TagSigned {
		return mismatch("source tag signature status", *tagObject.Verification.Verified, manifest.Source.TagSigned)
	}
	if commit.SHA != manifest.Source.Commit {
		return mismatch("source commit", commit.SHA, manifest.Source.Commit)
	}
	if commit.Tree.SHA != manifest.Source.Tree {
		return mismatch("source tree", commit.Tree.SHA, manifest.Source.Tree)
	}

	if ruleset.ID != manifest.Source.Protection.RulesetID {
		return mismatch("source protection ruleset ID", ruleset.ID, manifest.Source.Protection.RulesetID)
	}
	if ruleset.Name != manifest.Source.Protection.RulesetName {
		return mismatch("source protection ruleset name", ruleset.Name, manifest.Source.Protection.RulesetName)
	}
	if ruleset.Target != "tag" {
		return mismatch("source protection target", ruleset.Target, "tag")
	}
	if ruleset.Enforcement != "active" {
		return mismatch("source protection enforcement", ruleset.Enforcement, "active")
	}
	if ruleset.Conditions.RefName.Include == nil || len(*ruleset.Conditions.RefName.Include) != 1 || (*ruleset.Conditions.RefName.Include)[0] != "refs/tags/"+manifest.Source.Tag {
		return mismatch("source protection include", ruleset.Conditions.RefName.Include, []string{"refs/tags/" + manifest.Source.Tag})
	}
	if ruleset.Conditions.RefName.Exclude == nil || len(*ruleset.Conditions.RefName.Exclude) != 0 {
		return mismatch("source protection exclude", ruleset.Conditions.RefName.Exclude, []string{})
	}
	ruleTypes := make(map[string]bool, len(ruleset.Rules))
	for _, rule := range ruleset.Rules {
		ruleTypes[rule.Type] = true
	}
	for _, required := range []string{"update", "deletion"} {
		if !ruleTypes[required] {
			return fmt.Errorf("source protection rules mismatch: missing %s", required)
		}
	}
	if ruleset.BypassActors == nil || len(*ruleset.BypassActors) != 0 {
		return mismatch("source protection bypass actors", ruleset.BypassActors, []any{})
	}
	if ruleset.CurrentUserCanBypass != "never" {
		return mismatch("source protection current-user bypass", ruleset.CurrentUserCanBypass, "never")
	}
	return nil
}

// GitHubToken returns authentication for ruleset inspection. Environment
// variables take precedence; gh is only a local credential source fallback.
func GitHubToken(ctx context.Context) (string, error) {
	return resolveGitHubToken(os.Getenv, func() ([]byte, error) {
		return exec.CommandContext(ctx, "gh", "auth", "token").Output()
	})
}

func resolveGitHubToken(getenv func(string) string, ghAuthToken func() ([]byte, error)) (string, error) {
	if token := getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}
	if token := getenv("GH_TOKEN"); token != "" {
		return token, nil
	}
	output, err := ghAuthToken()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "", errors.New("GitHub authentication is required to verify that the protected tag has no bypass actors; set GITHUB_TOKEN/GH_TOKEN or run gh auth login")
	}
	return strings.TrimSpace(string(output)), nil
}

type baselinePackageMetadata struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	Repository struct {
		URL string `json:"url"`
	} `json:"repository"`
	Dist struct {
		Tarball      string `json:"tarball"`
		Integrity    string `json:"integrity"`
		Shasum       string `json:"shasum"`
		FileCount    int    `json:"fileCount"`
		UnpackedSize int64  `json:"unpackedSize"`
		Signatures   []struct {
			KeyID     string `json:"keyid"`
			Signature string `json:"sig"`
		} `json:"signatures"`
	} `json:"dist"`
}

type baselinePublicationMetadata struct {
	Time map[string]string `json:"time"`
}

type baselineGitObject struct {
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
}

type baselineTagObject struct {
	SHA    string `json:"sha"`
	Tag    string `json:"tag"`
	Object struct {
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"object"`
	Verification struct {
		Verified *bool `json:"verified"`
	} `json:"verification"`
}

type baselineCommit struct {
	SHA  string `json:"sha"`
	Tree struct {
		SHA string `json:"sha"`
	} `json:"tree"`
}

type baselineRuleset struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Target      string `json:"target"`
	Enforcement string `json:"enforcement"`
	Conditions  struct {
		RefName struct {
			Include *[]string `json:"include"`
			Exclude *[]string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type string `json:"type"`
	} `json:"rules"`
	BypassActors         *[]json.RawMessage `json:"bypass_actors"`
	CurrentUserCanBypass string             `json:"current_user_can_bypass"`
}

func getBaselineJSON(ctx context.Context, client *http.Client, resource, token string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resource, nil)
	if err != nil {
		return err
	}
	if strings.HasPrefix(resource, "https://api.github.com/") {
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "open-skills-baseline-verifier")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", resource, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("GET %s failed: %s", resource, response.Status)
	}
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode GET %s: %w", resource, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode GET %s: trailing JSON value", resource)
		}
		return fmt.Errorf("decode GET %s: trailing content: %w", resource, err)
	}
	return nil
}

func getBaselineBytes(ctx context.Context, client *http.Client, resource string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resource, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s failed: %w", resource, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s failed: %s", resource, response.Status)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read GET %s: %w", resource, err)
	}
	return data, nil
}

func mismatch(field string, actual, expected any) error {
	return fmt.Errorf("%s mismatch: expected %v, got %v", field, expected, actual)
}

func parsedRepositoryOrigin(repository *url.URL) string {
	if repository == nil || repository.Scheme == "" || repository.Host == "" {
		return ""
	}
	return repository.Scheme + "://" + repository.Host
}
