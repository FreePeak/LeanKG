package index

import (
	"testing"
)

func TestExtractMavenDependencies(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<project>
    <groupId>com.example</groupId>
    <artifactId>my-app</artifactId>
    <version>1.0.0</version>
    <dependencies>
        <dependency>
            <groupId>org.springframework.boot</groupId>
            <artifactId>spring-boot-starter-web</artifactId>
        </dependency>
        <dependency>
            <groupId>org.junit.jupiter</groupId>
            <artifactId>junit-jupiter</artifactId>
            <scope>test</scope>
        </dependency>
    </dependencies>
</project>`
	_, rels := ExtractMaven("pom.xml", []byte(src))
	if got := countRels(rels, "has_dependency"); got < 2 {
		t.Fatalf("has_dependency = %d, want >= 2", got)
	}
	// First dependency has no <scope>: defaults to compile.
	first := findRel(rels, "has_dependency")
	if first.Metadata["scope"] != "compile" {
		t.Fatalf("default scope = %v, want compile", first.Metadata["scope"])
	}
	if first.Target != "__dep__org.springframework.boot:spring-boot-starter-web" {
		t.Fatalf("target = %q", first.Target)
	}
	var testScoped bool
	for _, r := range rels {
		if r.RelType == "has_dependency" && r.Metadata["scope"] == "test" {
			testScoped = true
		}
	}
	if !testScoped {
		t.Fatal("missing test-scoped junit dependency")
	}
}

func TestExtractMavenProjectCoords(t *testing.T) {
	src := `<?xml version="1.0"?>
<project>
    <groupId>com.example</groupId>
    <artifactId>my-app</artifactId>
    <version>1.0.0</version>
    <packaging>jar</packaging>
</project>`
	els, _ := ExtractMaven("pom.xml", []byte(src))
	project := filterElems(els, "maven_project")
	if len(project) == 0 {
		t.Fatal("should extract Maven project")
	}
	if project[0].Metadata["groupId"] != "com.example" {
		t.Fatalf("groupId = %v", project[0].Metadata["groupId"])
	}
	if project[0].Metadata["artifactId"] != "my-app" {
		t.Fatalf("artifactId = %v", project[0].Metadata["artifactId"])
	}
	if project[0].QualifiedName != "__maven_project__com.example:my-app" {
		t.Fatalf("qualified name = %q", project[0].QualifiedName)
	}
}

func TestExtractMavenFixture(t *testing.T) {
	els, rels := ExtractMaven("service/pom.xml", mustReadFixture(t, "pom.xml"))
	project := findElem(els, "maven_project", "sample-service")
	if project == nil {
		t.Fatal("missing maven_project element")
	}
	if project.Metadata["packaging"] != "jar" {
		t.Fatalf("packaging = %v", project.Metadata["packaging"])
	}
	if got := countRels(rels, "has_dependency"); got != 2 {
		t.Fatalf("has_dependency = %d, want 2", got)
	}
	// Versions are Option<String> in Rust: absent -> null.
	for _, r := range rels {
		if r.Target == "__dep__org.junit.jupiter:junit-jupiter" && r.Metadata["version"] != nil {
			t.Fatalf("expected null version for junit-jupiter, got %v", r.Metadata["version"])
		}
		if r.Target == "__dep__org.springframework.boot:spring-boot-starter-web" && r.Metadata["version"] != "3.2.0" {
			t.Fatalf("spring version = %v, want 3.2.0", r.Metadata["version"])
		}
	}
}
