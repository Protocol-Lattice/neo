package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type commandConfig struct {
	dir             string
	out             string
	packageOverride string
	target          string
	tsRuntimeImport string
	metadataURL     string
}

func main() {
	if err := runNeoGen(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "neo-gen: %v\n", err)
		os.Exit(1)
	}
}

func runNeoGen(args []string, stdout io.Writer, stderr io.Writer) error {
	cfg, err := parseCommandConfig(args, stderr)
	if err != nil {
		return err
	}

	scan, err := loadPackageScan(cfg)
	if err != nil {
		return err
	}
	if len(scan.Procedures) == 0 {
		return fmt.Errorf(
			"no typed procedures found in %s; expected typed Register/RegisterSubscription calls or Gateway.Proxy calls with neo.WithProxyMetadata",
			cfg.dir,
		)
	}

	pkg := scan.Package
	if cfg.packageOverride != "" {
		pkg = cfg.packageOverride
	}

	src, err := generateTarget(scan, pkg, cfg)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.out), 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(cfg.out, src, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", cfg.out, err)
	}

	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "neo-gen: generated %d typed procedures in %s\n", len(scan.Procedures), cfg.out)
	}

	return nil
}

func parseCommandConfig(args []string, stderr io.Writer) (commandConfig, error) {
	cfg := commandConfig{}
	flags := flag.NewFlagSet("neo-gen", flag.ContinueOnError)
	if stderr != nil {
		flags.SetOutput(stderr)
	} else {
		flags.SetOutput(io.Discard)
	}

	flags.StringVar(&cfg.dir, "dir", ".", "directory containing procedure registrations")
	flags.StringVar(&cfg.out, "out", "neo.gen.go", "output file")
	flags.StringVar(&cfg.packageOverride, "package", "", "optional generated package name")
	flags.StringVar(&cfg.target, "target", "go", "generation target: go, ts, ts-runtime, ts-standalone, docs, schema, or openapi")
	flags.StringVar(&cfg.tsRuntimeImport, "ts-runtime-import", "./neo.runtime.ts", "runtime import path for generated TypeScript clients")
	flags.StringVar(&cfg.metadataURL, "metadata-url", "", "optional Neo metadata endpoint or base URL to generate from")

	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}

	return cfg, nil
}

func loadPackageScan(cfg commandConfig) (packageScan, error) {
	if cfg.metadataURL == "" {
		return scanPackage(cfg.dir)
	}

	metadata, err := fetchMetadataURL(cfg.metadataURL)
	if err != nil {
		return packageScan{}, err
	}

	scan, err := scanOptionalPackage(cfg.dir)
	if err != nil {
		return packageScan{}, err
	}
	scan.Procedures = metadataProcedures(metadata)
	return scan, nil
}

func scanOptionalPackage(dir string) (packageScan, error) {
	scan, err := scanPackage(dir)
	if err == nil {
		return scan, nil
	}
	if strings.Contains(err.Error(), "no go package found") {
		return packageScan{}, nil
	}
	return packageScan{}, err
}

type remoteProcedureMeta struct {
	Key         string   `json:"key"`
	Kind        string   `json:"kind"`
	Input       string   `json:"input"`
	Output      string   `json:"output"`
	Summary     string   `json:"summary,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Deprecated  bool     `json:"deprecated,omitempty"`
}

func fetchMetadataURL(rawURL string) ([]remoteProcedureMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataEndpointURL(rawURL), nil)
	if err != nil {
		return nil, fmt.Errorf("create metadata request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch metadata: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch metadata: status %d", res.StatusCode)
	}

	var metadata []remoteProcedureMeta
	if err := json.NewDecoder(res.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}

	return metadata, nil
}

func metadataEndpointURL(rawURL string) string {
	rawURL = strings.TrimRight(rawURL, "/")
	if strings.HasSuffix(rawURL, "/_meta") {
		return rawURL
	}
	return rawURL + "/_meta"
}

func metadataProcedures(metadata []remoteProcedureMeta) []procedure {
	procedures := make([]procedure, 0, len(metadata))
	for _, meta := range metadata {
		p := procedure{
			Key:         strings.Trim(meta.Key, "."),
			Kind:        meta.Kind,
			Input:       meta.Input,
			Output:      meta.Output,
			Summary:     meta.Summary,
			Description: meta.Description,
			Tags:        meta.Tags,
			Deprecated:  meta.Deprecated,
		}
		if p.Kind == "" {
			p.Kind = "query"
		}
		if p.Key == "" || p.Input == "" || p.Output == "" {
			continue
		}
		procedures = append(procedures, p)
	}
	return procedures
}

func generateTarget(scan packageScan, pkg string, cfg commandConfig) ([]byte, error) {
	switch strings.ToLower(cfg.target) {
	case "go":
		if pkg == "" {
			return nil, fmt.Errorf("could not determine package for %s", cfg.dir)
		}
		src, err := generate(pkg, scan.Procedures)
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	case "ts", "typescript":
		src, err := generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
			RuntimeImport: cfg.tsRuntimeImport,
		})
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	case "ts-runtime", "typescript-runtime":
		src, err := generateTypeScriptRuntime()
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	case "ts-standalone", "typescript-standalone":
		src, err := generateTypeScript(scan.Procedures, scan.Types, typeScriptOptions{
			Standalone: true,
		})
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	case "docs", "markdown", "md":
		src, err := generateDocs(scan.Procedures)
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	case "schema", "json-schema":
		src, err := generateSchema(scan.Procedures)
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	case "openapi", "openapi-json":
		src, err := generateOpenAPI(scan.Procedures, scan.Types)
		if err != nil {
			return src, fmt.Errorf("generate: %w", err)
		}
		return src, nil
	default:
		return nil, fmt.Errorf("unsupported target %q; expected go, ts, ts-runtime, ts-standalone, docs, schema, or openapi", cfg.target)
	}
}
