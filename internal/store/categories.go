package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mrfroglab/be-rongsok.in/internal/db"
)

const categoryCols = `id, name, description, "iconUrl", unit, "sortOrder", "parentId"`

func scanCategory(row pgx.Row) (*Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.Description, &c.IconURL, &c.Unit, &c.SortOrder, &c.ParentID)
	if IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// nullCategory = kolom kategori dari LEFT JOIN (bisa NULL semua).
type nullCategory struct {
	ID, Name, Unit       *string
	Description, IconURL *string
	SortOrder            *int
	ParentID             *string
}

func (n *nullCategory) targets() []any {
	return []any{&n.ID, &n.Name, &n.Description, &n.IconURL, &n.Unit, &n.SortOrder, &n.ParentID}
}

func (n *nullCategory) value() *Category {
	if n.ID == nil {
		return nil
	}
	return &Category{ID: *n.ID, Name: *n.Name, Description: n.Description, IconURL: n.IconURL,
		Unit: *n.Unit, SortOrder: *n.SortOrder, ParentID: n.ParentID}
}

// qualified mengembalikan daftar kolom kategori dengan alias tabel.
func categoryColsAs(alias string) string {
	return alias + `.id, ` + alias + `.name, ` + alias + `.description, ` + alias + `."iconUrl", ` +
		alias + `.unit, ` + alias + `."sortOrder", ` + alias + `."parentId"`
}

// ListCategories: flat list (FE membangun tree dari parentId).
func ListCategories(ctx context.Context, q db.DBTX) ([]Category, error) {
	rows, err := q.Query(ctx, `SELECT `+categoryCols+` FROM "WasteCategory" ORDER BY "sortOrder" ASC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func GetCategory(ctx context.Context, q db.DBTX, id string) (*Category, error) {
	return scanCategory(q.QueryRow(ctx, `SELECT `+categoryCols+` FROM "WasteCategory" WHERE id = $1`, id))
}

type NewCategory struct {
	Name        string
	Description *string
	IconURL     *string
	ParentID    *string
	Unit        *string
	SortOrder   *int
}

func CreateCategory(ctx context.Context, q db.DBTX, n NewCategory) (*Category, error) {
	unit := "kg"
	if n.Unit != nil {
		unit = *n.Unit
	}
	sort := 0
	if n.SortOrder != nil {
		sort = *n.SortOrder
	}
	return scanCategory(q.QueryRow(ctx, `
		INSERT INTO "WasteCategory" (id, name, description, "iconUrl", unit, "sortOrder", "parentId")
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+categoryCols,
		NewID(), n.Name, n.Description, n.IconURL, unit, sort, n.ParentID))
}

// UpdateCategory: nil bila kategori tidak ada.
func UpdateCategory(ctx context.Context, q db.DBTX, id string, s *Sets) (*Category, error) {
	if s.Empty() {
		return GetCategory(ctx, q, id)
	}
	args := append(s.Args, id)
	return scanCategory(q.QueryRow(ctx,
		`UPDATE "WasteCategory" SET `+s.String()+` WHERE id = $`+itoa(len(args))+` RETURNING `+categoryCols, args...))
}

// CategoryUsage: apakah kategori punya anak / dipakai katalog / dipakai item order.
func CategoryUsage(ctx context.Context, q db.DBTX, id string) (hasChildren, inUse bool, err error) {
	err = q.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM "WasteCategory" WHERE "parentId" = $1),
		EXISTS (SELECT 1 FROM "CollectorCatalog" WHERE "categoryId" = $1)
		OR EXISTS (SELECT 1 FROM "OrderItem" WHERE "categoryId" = $1)`, id).Scan(&hasChildren, &inUse)
	return
}

// DeleteCategory: false bila tidak ada.
func DeleteCategory(ctx context.Context, q db.DBTX, id string) (bool, error) {
	tag, err := q.Exec(ctx, `DELETE FROM "WasteCategory" WHERE id = $1`, id)
	return tag.RowsAffected() == 1, err
}

func CountCategories(ctx context.Context, q db.DBTX) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM "WasteCategory"`).Scan(&n)
	return n, err
}
