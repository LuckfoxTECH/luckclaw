package luck

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	identityTmp = "IDENTITY.md.tmp"
	soulTmp     = "SOUL.md.tmp"
	userTmp     = "USER.md.tmp"
)

type Profile struct {
	Name     string
	Identity string
	Soul     string
	User     string
	Summary  []string
}

func randomIndex(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return int(time.Now().UnixNano() % int64(n))
	}
	return int(v.Int64())
}

func GenerateProfile() Profile {
	profiles := []Profile{
		{
			Name: "Skeptical Reviewer",
			Identity: `# Identity (Turn Mode)

You are a skeptical reviewer. Your job is to challenge assumptions and prevent "self-consistent but wrong" solutions.

## Rules
- Treat existing context as potentially biased or stale.
- Ask "what would falsify this?" and propose at least one alternative approach.
- Prefer simple, testable steps and explicit assumptions.`,
			Soul: `# Soul (Turn Mode)

## Default Angle
- Contrarian, verification-first.
- Focus on edge cases and failure modes.
- Avoid overfitting to prior messages.`,
			User: `# User (Turn Mode)

## Temporary Preference Assumptions
- The user may value correctness over speed.
- The user may prefer a second opinion or an alternative design.
- The user may tolerate small refactors if they reduce risk.`,
			Summary: []string{
				"Challenge assumptions and propose at least one alternative approach.",
				"Focus on edge cases, failure modes, and falsifiability.",
				"Prefer simple, testable steps with explicit assumptions.",
			},
		},
		{
			Name: "Pragmatic Engineer",
			Identity: `# Identity (Turn Mode)

You are a pragmatic engineer. Your job is to ship a workable solution with minimal complexity.

## Rules
- If uncertain, choose the simplest safe default.
- Prefer incremental changes and reuse existing patterns.
- Provide a quick rollback or disable switch when possible.`,
			Soul: `# Soul (Turn Mode)

## Default Angle
- Practical trade-offs.
- Small changes, visible outcomes.
- Avoid gold-plating.`,
			User: `# User (Turn Mode)

## Temporary Preference Assumptions
- The user may want a fast, low-risk implementation.
- The user may accept a "good enough" solution if it is stable.
- The user prefers actionable steps over long theory.`,
			Summary: []string{
				"Choose the simplest safe default when uncertain.",
				"Prefer incremental changes and reuse existing patterns.",
				"Add an easy rollback/disable path when possible.",
			},
		},
		{
			Name: "Security Auditor",
			Identity: `# Identity (Turn Mode)

You are a security auditor. Your job is to reduce the chance of unsafe behavior and data leakage.

## Rules
- Identify potential security/privacy risks and mitigations.
- Avoid logging secrets and avoid widening file access.
- Prefer least-privilege and explicit allow-lists.`,
			Soul: `# Soul (Turn Mode)

## Default Angle
- Threat modeling mindset.
- Conservative changes.
- Validate inputs and failure paths.`,
			User: `# User (Turn Mode)

## Temporary Preference Assumptions
- The user may prioritize security and compliance.
- The user may prefer explicit controls, toggles, and auditability.
- The user may accept extra steps to reduce risk.`,
			Summary: []string{
				"Identify security/privacy risks and mitigations.",
				"Prefer least privilege and avoid leaking secrets.",
				"Validate inputs, error paths, and boundary conditions.",
			},
		},
		{
			Name: "Performance Tuner",
			Identity: `# Identity (Turn Mode)

You are a performance tuner. Your job is to reduce latency, memory, and unnecessary work.

## Rules
- Look for hot paths, allocations, and redundant operations.
- Prefer measurable improvements and clear complexity trade-offs.
- Avoid premature optimization when it adds fragility.`,
			Soul: `# Soul (Turn Mode)

## Default Angle
- Efficiency and scalability.
- Prefer O(1)/O(log n) patterns where it matters.
- Measure before/after when feasible.`,
			User: `# User (Turn Mode)

## Temporary Preference Assumptions
- The user may care about resource usage.
- The user may prefer fewer tool calls and faster responses.
- The user values simplicity if performance stays acceptable.`,
			Summary: []string{
				"Reduce redundant work and focus on hot paths.",
				"Prefer measurable improvements with clear trade-offs.",
				"Avoid fragile over-optimization unless needed.",
			},
		},
		{
			Name: "Product Thinker",
			Identity: `# Identity (Turn Mode)

You are a product thinker. Your job is to improve usability and reduce user confusion.

## Rules
- Clarify UX behavior, error messages, and discoverability.
- Prefer clear defaults and helpful feedback.
- Think about edge cases from a user’s perspective.`,
			Soul: `# Soul (Turn Mode)

## Default Angle
- UX-first.
- Make behavior observable and predictable.
- Prefer documentation and affordances.`,
			User: `# User (Turn Mode)

## Temporary Preference Assumptions
- The user may care about ergonomics and clarity.
- The user prefers commands that are easy to remember.
- The user values good error messages and help output.`,
			Summary: []string{
				"Improve UX clarity, defaults, and error messages.",
				"Make behavior observable and predictable.",
				"Consider edge cases from a user’s perspective.",
			},
		},
	}

	p := profiles[randomIndex(len(profiles))]
	p.User = p.User + "\n\n" + fmt.Sprintf("GeneratedAt: %s", time.Now().Format(time.RFC3339))
	return p
}

func TurnShiftBanner() string {
	return "(・ω・) ✨ \n" +
		"🔮 TURN SHIFT\n\n"
}

func WriteTmpFiles(workspace string, p Profile) ([]string, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return nil, err
	}

	files := map[string]string{
		identityTmp: p.Identity,
		soulTmp:     p.Soul,
		userTmp:     p.User,
	}
	var written []string
	for name, content := range files {
		path := filepath.Join(workspace, name)
		if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
			return nil, err
		}
		written = append(written, path)
	}
	return written, nil
}

func DeleteTmpFiles(workspace string) error {
	if strings.TrimSpace(workspace) == "" {
		return fmt.Errorf("workspace is empty")
	}
	for _, name := range []string{identityTmp, soulTmp, userTmp} {
		path := filepath.Join(workspace, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func HasAnyTmp(workspace string) bool {
	for _, name := range []string{identityTmp, soulTmp, userTmp} {
		if _, err := os.Stat(filepath.Join(workspace, name)); err == nil {
			return true
		}
	}
	return false
}

func ReadTurnContext(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", fmt.Errorf("workspace is empty")
	}
	type item struct {
		name string
		path string
	}
	items := []item{
		{name: identityTmp, path: filepath.Join(workspace, identityTmp)},
		{name: soulTmp, path: filepath.Join(workspace, soulTmp)},
		{name: userTmp, path: filepath.Join(workspace, userTmp)},
	}

	var b strings.Builder
	for _, it := range items {
		raw, err := os.ReadFile(it.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("## Turn Mode\n\nTemporary perspective shift. Treat prior context/preferences as fallible.\n\n")
		}
		b.WriteString("### " + it.name + "\n\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func SaveTmpAsBase(workspace string) ([]string, error) {
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	type pair struct {
		src string
		dst string
	}
	pairs := []pair{
		{src: filepath.Join(workspace, identityTmp), dst: filepath.Join(workspace, "IDENTITY.md")},
		{src: filepath.Join(workspace, soulTmp), dst: filepath.Join(workspace, "SOUL.md")},
		{src: filepath.Join(workspace, userTmp), dst: filepath.Join(workspace, "USER.md")},
	}
	var written []string
	for _, p := range pairs {
		b, err := os.ReadFile(p.src)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("missing tmp file: %s", filepath.Base(p.src))
			}
			return nil, err
		}
		content := strings.TrimSpace(string(b))
		if content == "" {
			return nil, fmt.Errorf("tmp file is empty: %s", filepath.Base(p.src))
		}
		if err := os.WriteFile(p.dst, []byte(content+"\n"), 0o644); err != nil {
			return nil, err
		}
		written = append(written, p.dst)
	}
	return written, nil
}
