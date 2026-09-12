package index

import (
	"testing"
)

func TestExtractHiltModule(t *testing.T) {
	src := `
            @Module
            @InstallIn(SingletonComponent::class)
            class AppModule {
                @Provides
                fun provideRepo(): Repository = RepositoryImpl()
            }
        `
	els, _ := ExtractHilt("./test.kt", []byte(src))
	modules := filterElems(els, "hilt_module")
	if len(modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(modules))
	}
	if modules[0].Name != "AppModule" {
		t.Fatalf("module name = %q", modules[0].Name)
	}
}

func TestExtractHiltProvider(t *testing.T) {
	src := `
            @Module
            class AppModule {
                @Provides
                @Singleton
                fun provideDatabase(): TvDatabase = Room.databaseBuilder(...).build()
            }
        `
	els, rels := ExtractHilt("./test.kt", []byte(src))
	providers := filterElems(els, "hilt_provider")
	if len(providers) == 0 {
		t.Fatal("expected a provider")
	}
	if providers[0].Name != "provideDatabase" {
		t.Fatalf("provider name = %q", providers[0].Name)
	}
	if got := countRels(rels, "hilt_provides"); got == 0 {
		t.Fatal("missing hilt_provides relationship")
	}
	if got := countRels(rels, "hilt_module_provides"); got != 1 {
		t.Fatalf("hilt_module_provides = %d, want 1", got)
	}
}

func TestExtractHiltInject(t *testing.T) {
	src := `
            class ViewModel @Inject constructor(
                private val repository: ChannelRepository
            )
        `
	_, rels := ExtractHilt("./test.kt", []byte(src))
	injected := filterRels(rels, "hilt_injected")
	if len(injected) == 0 {
		t.Fatal("expected hilt_injected relationship")
	}
	if injected[0].Target != "__type__ChannelRepository" {
		t.Fatalf("target = %q", injected[0].Target)
	}
	if injected[0].Source != "./test.kt::__class__ViewModel" {
		t.Fatalf("source = %q", injected[0].Source)
	}
}

func TestExtractHiltFixture(t *testing.T) {
	els, rels := ExtractHilt("ui/MainActivity.kt", mustReadFixture(t, "MainActivity.kt"))
	if got := countType(els, "hilt_module"); got != 1 {
		t.Fatalf("modules = %d, want 1", got)
	}
	if got := countType(els, "hilt_provider"); got != 1 {
		t.Fatalf("providers = %d, want 1", got)
	}
	if got := countRels(rels, "hilt_module_provides"); got != 1 {
		t.Fatalf("hilt_module_provides = %d, want 1", got)
	}
	if got := countRels(rels, "hilt_provides"); got != 1 {
		t.Fatalf("hilt_provides = %d, want 1", got)
	}
	if got := countRels(rels, "hilt_field_injected"); got != 1 {
		t.Fatalf("hilt_field_injected = %d, want 1", got)
	}
	injected := filterRels(rels, "hilt_injected")
	if len(injected) != 2 {
		t.Fatalf("hilt_injected = %d, want 2 (api + db)", len(injected))
	}
}
