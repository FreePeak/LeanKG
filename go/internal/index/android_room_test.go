package index

import (
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestExtractRoomEntity(t *testing.T) {
	src := `
            @Entity(tableName = "channels")
            data class ChannelEntity(
                @PrimaryKey val id: Long,
                val name: String
            )
        `
	els, _ := ExtractRoom("./test.kt", []byte(src))
	entities := filterElems(els, "room_entity")
	if len(entities) != 1 {
		t.Fatalf("entities = %d, want 1", len(entities))
	}
	if entities[0].Name != "ChannelEntity" {
		t.Fatalf("entity name = %q", entities[0].Name)
	}
	if entities[0].QualifiedName != "./test.kt::RoomEntity:ChannelEntity" {
		t.Fatalf("entity qn = %q", entities[0].QualifiedName)
	}
}

func TestExtractRoomDao(t *testing.T) {
	src := `
            @Dao
            interface ChannelDao {
                @Query("SELECT * FROM channels")
                fun getAll(): List<ChannelEntity>
            }
        `
	els, rels := ExtractRoom("./test.kt", []byte(src))
	daos := filterElems(els, "room_dao")
	if len(daos) != 1 || daos[0].Name != "ChannelDao" {
		t.Fatalf("daos = %v", daos)
	}
	query := findRel(rels, "room_dao_queries_entity")
	if query == nil {
		t.Fatal("missing room_dao_queries_entity")
	}
	if query.Target != "./test.kt::RoomEntity:channels" {
		t.Fatalf("query target = %q", query.Target)
	}
}

func TestExtractRoomDatabase(t *testing.T) {
	src := `
            @Database(entities = [ChannelEntity::class, VodEntity::class], version = 1)
            abstract class TvDatabase : RoomDatabase()
        `
	els, rels := ExtractRoom("./test.kt", []byte(src))
	dbs := filterElems(els, "room_database")
	if len(dbs) != 1 || dbs[0].Name != "TvDatabase" {
		t.Fatalf("databases = %v", dbs)
	}
	if got := countRels(rels, "room_database_contains_entity"); got != 2 {
		t.Fatalf("contains_entity = %d, want 2", got)
	}
}

// TestExtractRoomForeignKey covers the path the Rust FK scan detects: the
// ForeignKey(...) declaration must sit after the entity class header, inside
// the scanned class body. Parity quirks: the scan window starts at the class
// header (so annotation-style foreignKeys are invisible), and the column
// regexes are `\[(\w+)\]` (so quoted column lists, the valid Kotlin form, do
// not match).
func TestExtractRoomForeignKey(t *testing.T) {
	src := `
            @Entity(tableName = "programs")
            data class ProgramEntity(
                @PrimaryKey val id: Long
            ) {
                val fk = ForeignKey(
                    entity = ChannelEntity::class,
                    parentColumns = [id],
                    childColumns = [channelId]
                )
            }
        `
	_, rels := ExtractRoom("data/Programs.kt", []byte(src))
	fk := findRel(rels, "room_entity_has_foreign_key")
	if fk == nil {
		t.Fatal("missing room_entity_has_foreign_key")
	}
	if fk.Source != "data/Programs.kt::RoomEntity:ProgramEntity" {
		t.Fatalf("fk source = %q", fk.Source)
	}
	if fk.Target != "__room_entity__ChannelEntity" {
		t.Fatalf("fk target = %q", fk.Target)
	}
	if fk.Confidence != 0.9 {
		t.Fatalf("fk confidence = %v, want 0.9", fk.Confidence)
	}
}

func TestExtractRoomFixture(t *testing.T) {
	els, rels := ExtractRoom("data/AppDatabase.kt", mustReadFixture(t, "AppDatabase.kt"))
	if got := countType(els, "room_entity"); got != 2 {
		t.Fatalf("entities = %d, want 2", got)
	}
	if got := countType(els, "room_dao"); got != 1 {
		t.Fatalf("daos = %d, want 1", got)
	}
	if got := countType(els, "room_database"); got != 1 {
		t.Fatalf("databases = %d, want 1", got)
	}
	// Parity: the FK scan starts at the entity class header, so the fixture's
	// Room `@Entity(foreignKeys = …)` annotation yields no edge.
	if got := countRels(rels, "room_entity_has_foreign_key"); got != 0 {
		t.Fatalf("foreign keys = %d, want 0 for annotation-style foreign keys", got)
	}
	if got := countRels(rels, "room_database_contains_entity"); got != 2 {
		t.Fatalf("contains_entity = %d, want 2", got)
	}
	if got := countRels(rels, "room_database_contains_dao"); got != 1 {
		t.Fatalf("contains_dao = %d, want 1", got)
	}
	if got := countRels(rels, "room_dao_queries_entity"); got != 1 {
		t.Fatalf("dao queries = %d, want 1", got)
	}
}

func TestFindClassBodyEnd(t *testing.T) {
	content := "class Foo { fun bar() { } }"
	start := strings.Index(content, "class Foo")
	if got := findClassBodyEnd(content, start); got != len(content) {
		t.Fatalf("nested braces end = %d, want %d", got, len(content))
	}
	content = "class Foo { val x = 1"
	if got := findClassBodyEnd(content, 0); got != len(content) {
		t.Fatalf("unbalanced end = %d, want %d", got, len(content))
	}
}

func filterElems(els []store.Element, typ string) []store.Element {
	var out []store.Element
	for _, e := range els {
		if e.ElementType == typ {
			out = append(out, e)
		}
	}
	return out
}
