package playsql

// Model is the optional embeddable base. Embed it to get persistence-state
// tracking — whether the row exists yet (insert vs update) and which columns are
// dirty (so Update writes only what changed):
//
//	type User struct {
//		playsql.Model
//		ID   int64  `db:"id" play:"pk,incrementing"`
//		Name string `db:"name"`
//	}
//
// Plain structs without the base still work; they fall back to a zero-key
// heuristic for Save and update every column.
type Model struct {
	exists   bool
	original map[string]any
}

// baseAccessor is satisfied by any type embedding Model (methods are promoted
// through the embedded value). It lets the package read/write persistence state
// without reflecting over unexported fields.
type baseAccessor interface {
	playExists() bool
	playMarkPersisted(original map[string]any)
	playOriginal() map[string]any
}

func (m *Model) playExists() bool { return m.exists }

func (m *Model) playMarkPersisted(original map[string]any) {
	m.exists = true
	m.original = original
}

func (m *Model) playOriginal() map[string]any { return m.original }

// baseOf returns the embedded Model accessor if the value has one.
func baseOf(model any) (baseAccessor, bool) {
	acc, ok := model.(baseAccessor)
	return acc, ok
}
