package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/MatHoyer/kipitiny/internal/store"
)

// DocChanges are edits to one collection's documents, in relaxed Extended
// JSON as the browser shows them. They run deletes first, then updates,
// then inserts. An update sets and unsets top-level fields only, so the
// fields it doesn't touch keep their exact BSON types.
type DocChanges struct {
	Deletes []DocDelete `json:"deletes,omitempty"`
	Updates []DocUpdate `json:"updates,omitempty"`
	// Inserts are whole documents.
	Inserts []string `json:"inserts,omitempty"`
}

type DocDelete struct {
	// ID is the document's _id.
	ID string `json:"id"`
}

type DocUpdate struct {
	ID    string            `json:"id"`
	Set   map[string]string `json:"set,omitempty"`
	Unset []string          `json:"unset,omitempty"`
	// Was holds every set or unset field's value as loaded; nil when the
	// field was absent.
	Was map[string]*string `json:"was"`
}

// mongoChangesMax caps a save's operations as passed to mongosh: the
// environment holds them, and one variable can't pass 128 KiB.
const mongoChangesMax = 120 << 10

// mongoIdentRe is a collection name that reads as db.name.
var mongoIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// mongoApplyJS checks every update and delete finds its document, then runs
// them in order. A standalone server has no transactions: a failure leaves
// the changes before it saved, and the message says how many.
const mongoApplyJS = `const e = process.env, coll = db.getSiblingDB(e.KP_DB).getCollection(e.KP_COLL);
const ops = JSON.parse(e.KP_OPS).map((o) => ({ ...o, filter: o.filter && EJSON.parse(o.filter), update: o.update && EJSON.parse(o.update), doc: o.doc && EJSON.parse(o.doc) }));
const changed = (o) => new Error(o.label + " changed or was deleted since it was loaded; reload and make the change again");
for (const o of ops) if (o.filter && coll.countDocuments(o.filter, { limit: 2 }) !== 1) throw changed(o);
let done = 0;
try {
  for (const o of ops) {
    if (o.op === "delete") { if (coll.deleteOne(o.filter).deletedCount !== 1) throw changed(o); }
    else if (o.op === "update") { if (coll.updateOne(o.filter, o.update).matchedCount !== 1) throw changed(o); }
    else coll.insertOne(o.doc);
    done++;
  }
} catch (err) {
  throw new Error(err.message + (done === 0 ? "" : done === 1 ? " (the change before it was saved)" : " (the " + done + " changes before it were saved)"));
}`

// mongoOp is one operation of a save, for mongoApplyJS: its filter, update
// and document are Extended JSON.
type mongoOp struct {
	Op     string `json:"op"`
	Label  string `json:"label,omitempty"`
	Filter string `json:"filter,omitempty"`
	Update string `json:"update,omitempty"`
	Doc    string `json:"doc,omitempty"`
	shown  string
}

// ApplyDocChanges saves edits to a MongoDB collection, or with apply unset
// only returns what it would run, as mongosh calls. Admin only.
func (c *Core) ApplyDocChanges(ctx context.Context, id, database, collection string, ch DocChanges, apply bool) (res ChangeResult, err error) {
	if err := c.requireOn(ctx, id, PermDataEdit); err != nil {
		return ChangeResult{}, err
	}
	if apply {
		defer func() {
			status := http.StatusOK
			if err != nil {
				status = http.StatusBadRequest
			}
			c.Audit(ctx, "data edit", id, status, err)
		}()
	}
	t, err := c.databaseTarget(ctx, id, database)
	if err != nil {
		return ChangeResult{}, err
	}
	if t.svc.Kind != store.ServiceKindMongoDB {
		return ChangeResult{}, fmt.Errorf("%w: %s is not a mongodb database", ErrInvalid, t.svc.Name)
	}
	tables, err := t.mongoCollections(ctx)
	if err != nil {
		return ChangeResult{}, err
	}
	i := slices.IndexFunc(tables, func(tb PgTable) bool { return tb.Name == collection })
	if i < 0 {
		return ChangeResult{}, fmt.Errorf("collection %s.%s: %w", t.db, collection, store.ErrNotFound)
	}
	if tables[i].Kind != "collection" {
		return ChangeResult{}, fmt.Errorf("%w: %s is a %s: only a collection's documents can be edited", ErrInvalid, collection, tables[i].Kind)
	}
	ops, err := mongoOps(collection, ch)
	if err != nil {
		return ChangeResult{}, err
	}
	res.Statements = make([]string, len(ops))
	for i, op := range ops {
		res.Statements[i] = op.shown
	}
	if !apply {
		return res, nil
	}
	payload, err := json.Marshal(ops)
	if err != nil {
		return ChangeResult{}, err
	}
	if len(payload) > mongoChangesMax {
		return ChangeResult{}, fmt.Errorf("%w: these changes are too big to save at once; save them in smaller batches", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, dataConsoleTimeout+10*time.Second)
	defer cancel()
	err = t.dk.Exec(ctx, t.container, t.mongosh(mongoApplyJS, true, dataConsoleTimeout,
		"KP_DB="+t.db, "KP_COLL="+collection, "KP_OPS="+string(payload)))
	if err = mongoError(clientError(err)); err != nil {
		return ChangeResult{}, fmt.Errorf("%w: %s", ErrInvalid, mongoshMessage(err.Error()))
	}
	res.Applied = true
	return res, nil
}

// mongoshMessage drops the "Error: " mongosh puts before a thrown error's
// message.
func mongoshMessage(msg string) string {
	return strings.TrimPrefix(strings.TrimPrefix(msg, ErrInvalid.Error()+": "), "Error: ")
}

// mongoOps checks changes and turns them into operations. Values are only
// checked to be JSON here: mongosh parses them as Extended JSON.
func mongoOps(collection string, ch DocChanges) ([]mongoOp, error) {
	if n := len(ch.Deletes) + len(ch.Updates) + len(ch.Inserts); n == 0 {
		return nil, fmt.Errorf("%w: no changes to save", ErrInvalid)
	} else if n > dataChangesMax {
		return nil, fmt.Errorf("%w: %d changes at once is too many; save at most %d", ErrInvalid, n, dataChangesMax)
	}
	coll := "db.getCollection(" + jsonString(collection) + ")"
	if mongoIdentRe.MatchString(collection) {
		coll = "db." + collection
	}
	value := func(v, what string) (string, error) {
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(v)); err != nil {
			return "", fmt.Errorf("%w: %s is not JSON: %v", ErrInvalid, what, err)
		}
		return b.String(), nil
	}
	field := func(name string) error {
		if name == "" || name == "_id" || strings.HasPrefix(name, "$") || strings.Contains(name, ".") {
			return fmt.Errorf("%w: field %q can't be edited here: change it from the console", ErrInvalid, name)
		}
		return nil
	}
	// object writes fields, in order, as a JSON object.
	object := func(fields [][2]string) string {
		parts := make([]string, len(fields))
		for i, f := range fields {
			parts[i] = jsonString(f[0]) + ": " + f[1]
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	idOf := func(id string) (string, string, error) {
		v, err := value(id, "the _id")
		if err != nil {
			return "", "", err
		}
		return v, "document " + v, nil
	}

	var ops []mongoOp
	for _, d := range ch.Deletes {
		id, label, err := idOf(d.ID)
		if err != nil {
			return nil, err
		}
		filter := object([][2]string{{"_id", id}})
		ops = append(ops, mongoOp{Op: "delete", Label: label, Filter: filter, shown: fmt.Sprintf("%s.deleteOne(%s)", coll, filter)})
	}
	for _, u := range ch.Updates {
		id, label, err := idOf(u.ID)
		if err != nil {
			return nil, err
		}
		if len(u.Set)+len(u.Unset) == 0 {
			return nil, fmt.Errorf("%w: an update of %s changes no field", ErrInvalid, label)
		}
		names := make([]string, 0, len(u.Set))
		for name := range u.Set {
			names = append(names, name)
		}
		slices.Sort(names)
		filter := [][2]string{{"_id", id}}
		var set, unset [][2]string
		for _, name := range slices.Concat(names, u.Unset) {
			if err := field(name); err != nil {
				return nil, err
			}
			was, ok := u.Was[name]
			if !ok {
				return nil, fmt.Errorf("%w: an update of %s lacks %s's loaded value", ErrInvalid, label, name)
			}
			cond := `{"$exists": false}`
			if was != nil {
				if cond, err = value(*was, name+"'s loaded value"); err != nil {
					return nil, err
				}
			}
			filter = append(filter, [2]string{name, cond})
		}
		for _, name := range names {
			v, err := value(u.Set[name], name)
			if err != nil {
				return nil, err
			}
			set = append(set, [2]string{name, v})
		}
		for _, name := range u.Unset {
			if _, ok := u.Set[name]; ok {
				return nil, fmt.Errorf("%w: %s is both set and unset", ErrInvalid, name)
			}
			unset = append(unset, [2]string{name, `""`})
		}
		var update [][2]string
		if len(set) > 0 {
			update = append(update, [2]string{"$set", object(set)})
		}
		if len(unset) > 0 {
			update = append(update, [2]string{"$unset", object(unset)})
		}
		f, up := object(filter), object(update)
		ops = append(ops, mongoOp{Op: "update", Label: label, Filter: f, Update: up, shown: fmt.Sprintf("%s.updateOne(%s, %s)", coll, f, up)})
	}
	for _, doc := range ch.Inserts {
		v, err := value(doc, "a new document")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(v, "{") {
			return nil, fmt.Errorf("%w: a new document is a JSON object", ErrInvalid)
		}
		ops = append(ops, mongoOp{Op: "insert", Doc: v, shown: fmt.Sprintf("%s.insertOne(%s)", coll, v)})
	}
	return ops, nil
}

// jsonString quotes s as JSON, < > & as they are.
func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
