package index

import (
	"fmt"
	"regexp"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Port of src/indexer/android_room.rs (Rust reference f7624143^): Room
// database patterns — @Entity data classes, @Dao interfaces, @Database
// abstract classes, foreign keys, and DAO queries.

var (
	roomEntityRe   = regexp.MustCompile(`(?s)@Entity\s*(?:\(.*?\))?\s*data\s+class\s+(\w+)`)
	roomDaoRe      = regexp.MustCompile(`@Dao\s*\n?\s*\n?(?:interface|class)\s+(\w+)`)
	roomDatabaseRe = regexp.MustCompile(`@Database\s*\([^)]*\)\s*\n?\s*abstract\s+class\s+(\w+)`)
	roomFKRe       = regexp.MustCompile(`ForeignKey\s*\(\s*entity\s*=\s*(\w+)::class[^)]+parentColumns\s*=\s*\[(\w+)\][^)]+childColumns\s*=\s*\[(\w+)\]`)
	roomEntitiesRe = regexp.MustCompile(`entities\s*=\s*\[([^\]]+)\]`)
	roomClassRefRe = regexp.MustCompile(`(\w+)::class`)
	roomQueryRe    = regexp.MustCompile(`@Query\s*\(\s*"([^"]+)"\s*\)`)
	roomFromRe     = regexp.MustCompile(`(?i)FROM\s+(\w+)`)
)

// ExtractRoom extracts Room entities, DAOs and databases with their
// relationships from Kotlin source.
func ExtractRoom(filePath string, src []byte) ([]store.Element, []store.Relationship) {
	content := string(src)
	var elements []store.Element
	var relationships []store.Relationship

	roomElement := func(qn, elemType, name string, meta map[string]any) store.Element {
		return store.Element{
			QualifiedName: qn, ElementType: elemType, Name: name,
			FilePath: filePath, Language: "kotlin", Metadata: meta,
		}
	}

	var entities, daos, databases []store.Element
	for _, m := range roomEntityRe.FindAllStringSubmatch(content, -1) {
		entities = append(entities, roomElement(
			filePath+"::RoomEntity:"+m[1], "room_entity", m[1],
			map[string]any{"class_name": m[1]}))
	}
	for _, m := range roomDaoRe.FindAllStringSubmatch(content, -1) {
		daos = append(daos, roomElement(
			filePath+"::RoomDao:"+m[1], "room_dao", m[1],
			map[string]any{"interface_name": m[1]}))
	}
	for _, m := range roomDatabaseRe.FindAllStringSubmatch(content, -1) {
		databases = append(databases, roomElement(
			filePath+"::RoomDatabase:"+m[1], "room_database", m[1],
			map[string]any{"class_name": m[1]}))
	}

	elements = append(elements, entities...)
	elements = append(elements, daos...)
	elements = append(elements, databases...)

	// Foreign keys: scan each entity's class body.
	for _, entity := range entities {
		re, err := regexp.Compile(`(?:data\s+)?class\s+` + regexp.QuoteMeta(entity.Name))
		if err != nil {
			continue
		}
		loc := re.FindStringIndex(content)
		if loc == nil {
			continue
		}
		end := findClassBodyEnd(content, loc[0])
		body := content[loc[0]:end]
		for _, m := range roomFKRe.FindAllStringSubmatch(body, -1) {
			relationships = append(relationships, store.Relationship{
				Source:  filePath + "::RoomEntity:" + entity.Name,
				Target:  "__room_entity__" + m[1],
				RelType: "room_entity_has_foreign_key", Confidence: 0.9,
			})
		}
	}

	// Database content relationships.
	for _, db := range databases {
		if m := roomEntitiesRe.FindStringSubmatch(content); m != nil {
			for _, em := range roomClassRefRe.FindAllStringSubmatch(m[1], -1) {
				relationships = append(relationships, store.Relationship{
					Source:  db.QualifiedName,
					Target:  filePath + "::RoomEntity:" + em[1],
					RelType: "room_database_contains_entity", Confidence: 1.0,
				})
			}
		}
		// Heuristic: DAOs are linked by same-file presence (may produce false
		// positives with multiple databases per file; ported as-is).
		for _, dao := range daos {
			relationships = append(relationships, store.Relationship{
				Source: db.QualifiedName, Target: dao.QualifiedName,
				RelType: "room_database_contains_dao", Confidence: 0.7,
				Metadata: map[string]any{
					"heuristic": "same_file_presence",
					"note":      "DAO linked to Database by co-location; false positives possible",
				},
			})
		}
	}

	// DAO queries: every @Query in the file is checked for FROM <table>.
	for _, dao := range daos {
		for _, qm := range roomQueryRe.FindAllStringSubmatch(content, -1) {
			if fm := roomFromRe.FindStringSubmatch(qm[1]); fm != nil {
				relationships = append(relationships, store.Relationship{
					Source:  dao.QualifiedName,
					Target:  fmt.Sprintf("%s::RoomEntity:%s", filePath, fm[1]),
					RelType: "room_dao_queries_entity", Confidence: 0.8,
					Metadata: map[string]any{"query": qm[1]},
				})
			}
		}
	}

	return elements, relationships
}

// findClassBodyEnd ports kotlin_utils::find_class_body_end: returns the byte
// offset just past the closing brace of the class body starting at classStart
// (content length when unbalanced).
func findClassBodyEnd(content string, classStart int) int {
	depth := 0
	foundOpen := false
	for i := classStart; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
			foundOpen = true
		case '}':
			depth--
			if foundOpen && depth == 0 {
				return i + 1
			}
		}
	}
	return len(content)
}
