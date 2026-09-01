package topology

import (
	"context"
	"path/filepath"
	"sort"
)

type jvmProvider struct{}

func init() {
	RegisterProvider(jvmProvider{})
}

func (jvmProvider) Name() string    { return "jvm" }
func (jvmProvider) Version() string { return "1" }
func (jvmProvider) Languages() []string {
	return []string{"java", "kotlin", "kt", "scala"}
}
func (jvmProvider) Manifests() ManifestSelector {
	return ManifestSelector{Names: []string{
		"build.gradle",
		"build.gradle.kts",
		"build.sbt",
		"pom.xml",
		"settings.gradle",
		"settings.gradle.kts",
	}}
}

func (jvmProvider) Build(ctx context.Context, inventory Inventory) (Fragment, error) {
	if err := ctx.Err(); err != nil {
		return Fragment{}, err
	}
	var gradleManifests, mavenManifests, sbtManifests []string
	for _, manifest := range inventory.Manifests {
		switch filepath.Base(manifest) {
		case "settings.gradle", "settings.gradle.kts", "build.gradle", "build.gradle.kts":
			gradleManifests = append(gradleManifests, manifest)
		case "pom.xml":
			mavenManifests = append(mavenManifests, manifest)
		case "build.sbt":
			sbtManifests = append(sbtManifests, manifest)
		}
	}
	sort.Strings(gradleManifests)
	sort.Strings(mavenManifests)
	sort.Strings(sbtManifests)
	if len(gradleManifests) == 0 && len(mavenManifests) == 0 && len(sbtManifests) == 0 {
		return Fragment{
			Provider: "jvm",
			Coverage: Coverage{Status: CoverageUnavailable},
		}, nil
	}

	fragments := make([]Fragment, 0, 3)
	if len(gradleManifests) > 0 {
		fragment, err := buildGradleFragment(ctx, inventory, gradleManifests)
		if err != nil {
			return Fragment{}, err
		}
		fragments = append(fragments, fragment)
	}
	if len(mavenManifests) > 0 {
		fragment, err := buildMavenFragment(ctx, inventory, mavenManifests)
		if err != nil {
			return Fragment{}, err
		}
		fragments = append(fragments, fragment)
	}
	if len(sbtManifests) > 0 {
		fragment, err := buildSBTFragment(ctx, inventory, sbtManifests)
		if err != nil {
			return Fragment{}, err
		}
		fragments = append(fragments, fragment)
	}
	return combineProviderFragments("jvm", fragments), nil
}

func combineProviderFragments(provider string, fragments []Fragment) Fragment {
	combined := Fragment{
		Provider: provider,
		Members:  make(map[ID][]string),
		Coverage: Coverage{Status: CoverageComplete},
	}
	for _, fragment := range fragments {
		combined.Nodes = append(combined.Nodes, fragment.Nodes...)
		combined.Edges = append(combined.Edges, fragment.Edges...)
		for id, members := range fragment.Members {
			combined.Members[id] = append(combined.Members[id], members...)
		}
		combined.Coverage.Issues = append(combined.Coverage.Issues, fragment.Coverage.Issues...)
		if fragment.Coverage.Status != CoverageComplete {
			combined.Coverage.Status = CoveragePartial
		}
	}
	if len(combined.Coverage.Issues) > 0 {
		combined.Coverage.Status = CoveragePartial
	}
	return combined
}
