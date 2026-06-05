package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type commandConfig struct {
	dir             string
	out             string
	packageOverride string
	target          string
	tsRuntimeImport string
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

	scan, err := scanPackage(cfg.dir)
	if err != nil {
		return err
	}
	if len(scan.Procedures) == 0 {
		return fmt.Errorf(
			"no typed procedures found in %s; expected neo.Query[In, Out](...), neo.Mutation[In, Out](...), or neo.Subscription[In, Out](...) inside Register/RegisterSubscription",
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
	flags.StringVar(&cfg.target, "target", "go", "generation target: go, ts, ts-runtime, or ts-standalone")
	flags.StringVar(&cfg.tsRuntimeImport, "ts-runtime-import", "./neo.runtime.ts", "runtime import path for generated TypeScript clients")

	if err := flags.Parse(args); err != nil {
		return commandConfig{}, err
	}

	return cfg, nil
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
	default:
		return nil, fmt.Errorf("unsupported target %q; expected go, ts, ts-runtime, or ts-standalone", cfg.target)
	}
}
