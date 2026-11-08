package server

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// 订阅的自定义类别（修订第 314 条，参考 Wallos）：在内置类别之外自己添加，最多 30 个。
// 订阅的 category 为 "c<编号>"；颜色从内置类别的颜色中选（design/tokens.json 的 cat-*，存类别键），不另设颜色。
// 删除类别时，用到它的订阅改为“其他”。公开展示页把自定义类别显示为“其他”。

// SubCategoryItem 是一个自定义类别。
type SubCategoryItem struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`   // 订阅中使用的类别键：c<编号>
	Name  string `json:"name"`  // 1～16 个字符
	Color string `json:"color"` // 内置类别键之一，取它的颜色
}

const maxSubCategories = 30

var customCatKey = regexp.MustCompile(`^c[1-9][0-9]{0,8}$`)

// isCustomCategory 判断类别键是否为自定义类别的格式（是否存在另查）。
func isCustomCategory(k string) bool { return customCatKey.MatchString(k) }

// ListSubCategories 返回全部自定义类别（按编号）。
func (s *Store) ListSubCategories() ([]SubCategoryItem, error) {
	rows, err := s.DB.Query(`SELECT id, name, color FROM sub_categories ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubCategoryItem{}
	for rows.Next() {
		var x SubCategoryItem
		if err := rows.Scan(&x.ID, &x.Name, &x.Color); err != nil {
			return nil, err
		}
		x.Key = "c" + strconv.FormatInt(x.ID, 10)
		out = append(out, x)
	}
	return out, rows.Err()
}

// subCategoryExists 判断自定义类别键对应的类别是否存在。
func (s *Store) subCategoryExists(key string) (bool, error) {
	if !isCustomCategory(key) {
		return false, nil
	}
	id, _ := strconv.ParseInt(key[1:], 10, 64)
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM sub_categories WHERE id = ?`, id).Scan(&n)
	return n > 0, err
}

var errNoSubCategory = errorf(CodeNotFound, "类别不存在或已删除")

func validateSubCategory(x *SubCategoryItem) []FieldError {
	var fe []FieldError
	x.Name, x.Color = strings.TrimSpace(x.Name), strings.TrimSpace(x.Color)
	if n := utf8.RuneCountInString(x.Name); n == 0 || n > 16 || strings.ContainsFunc(x.Name, unicode.IsControl) {
		fe = append(fe, FieldError{Field: "name", Message: "请填写类别名称（最多 16 个字符）"})
	}
	if !slices.Contains(subCategories, x.Color) {
		fe = append(fe, FieldError{Field: "color", Message: "请选择颜色"})
	}
	return fe
}

// handleSubCategories：GET /api/v1/subscription-categories，admin。
func (s *Server) handleSubCategories(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListSubCategories()
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	writeList(w, list, "", nil)
}

// handleCreateSubCategory：POST /api/v1/subscription-categories，admin。{name, color}；成功 201。
func (s *Server) handleCreateSubCategory(w http.ResponseWriter, r *http.Request) {
	var x SubCategoryItem
	if err := decodeJSON(w, r, &x); err != nil {
		s.writeError(w, r, err)
		return
	}
	if fe := validateSubCategory(&x); fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	var n int
	if err := s.store.DB.QueryRow(`SELECT COUNT(*) FROM sub_categories`).Scan(&n); err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n >= maxSubCategories {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: []FieldError{{Field: "name", Message: fmt.Sprintf("最多 %d 个自定义类别", maxSubCategories)}}})
		return
	}
	res, err := s.store.DB.Exec(`INSERT INTO sub_categories (name, color, created_at) VALUES (?,?,?)`, x.Name, x.Color, time.Now().Unix())
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	x.ID, _ = res.LastInsertId()
	x.Key = "c" + strconv.FormatInt(x.ID, 10)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription_category.create", Success: true, Details: map[string]any{"id": x.ID, "name": x.Name}})
	writeJSONStatus(w, http.StatusCreated, x)
}

// handleUpdateSubCategory：PUT /api/v1/subscription-categories/{id}，admin。{name, color}。
func (s *Server) handleUpdateSubCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	var x SubCategoryItem
	if err := decodeJSON(w, r, &x); err != nil {
		s.writeError(w, r, err)
		return
	}
	if fe := validateSubCategory(&x); fe != nil {
		s.writeError(w, r, &APIError{Code: CodeValidationFailed, Details: fe})
		return
	}
	res, err := s.store.DB.Exec(`UPDATE sub_categories SET name = ?, color = ? WHERE id = ?`, x.Name, x.Color, id)
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeError(w, r, errNoSubCategory)
		return
	}
	x.ID, x.Key = id, "c"+strconv.FormatInt(id, 10)
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription_category.update", Success: true, Details: map[string]any{"id": id, "name": x.Name}})
	writeJSON(w, x)
}

// handleDeleteSubCategory：DELETE /api/v1/subscription-categories/{id}，admin。用到它的订阅改为“其他”，成功 204。
func (s *Server) handleDeleteSubCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	key := "c" + strconv.FormatInt(id, 10)
	var moved int64
	err = s.store.withTx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM sub_categories WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNoSubCategory
		}
		res, err = tx.Exec(`UPDATE subscriptions SET category = 'other', updated_at = ? WHERE category = ?`, time.Now().Unix(), key)
		if err != nil {
			return err
		}
		moved, _ = res.RowsAffected()
		return nil
	})
	if errors.Is(err, errNoSubCategory) {
		s.writeError(w, r, errNoSubCategory)
		return
	}
	if err != nil {
		s.writeError(w, r, internalError(err))
		return
	}
	s.audit(r, AuditEntry{ActorType: "admin", Action: "subscription_category.delete", Success: true, Details: map[string]any{"id": id, "moved": moved}})
	w.WriteHeader(http.StatusNoContent)
}

// withTx 在一个事务中执行 fn；fn 返回错误时回滚。
func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
