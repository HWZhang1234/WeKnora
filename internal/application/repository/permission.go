package repository

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// permissionRepository is the GORM-backed implementation of the usid
// permission layer (kb_acl / super_user / common_kb).
type permissionRepository struct {
	db *gorm.DB
}

// NewPermissionRepository constructs the GORM-backed implementation.
func NewPermissionRepository(db *gorm.DB) interfaces.PermissionRepository {
	return &permissionRepository{db: db}
}

// ---- kb_acl ----

// UpsertKBAcl inserts the grant, or updates the role if (kb_id, usid) already
// exists. Relies on the uq_kb_acl_kb_usid unique constraint.
func (r *permissionRepository) UpsertKBAcl(ctx context.Context, kbID, usid, role string) error {
	rec := &types.KBAcl{KBID: kbID, Usid: usid, Role: role}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "kb_id"}, {Name: "usid"}},
			DoUpdates: clause.AssignmentColumns([]string{"role", "updated_at"}),
		}).
		Create(rec).Error
}

func (r *permissionRepository) RemoveKBAcl(ctx context.Context, kbID, usid string) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("kb_id = ? AND usid = ?", kbID, usid).
		Delete(&types.KBAcl{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *permissionRepository) ListKBAclByKB(ctx context.Context, kbID string) ([]*types.KBAcl, error) {
	var list []*types.KBAcl
	err := r.db.WithContext(ctx).
		Where("kb_id = ?", kbID).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

func (r *permissionRepository) ListKBAclByUsid(ctx context.Context, usid string) ([]*types.KBAcl, error) {
	var list []*types.KBAcl
	err := r.db.WithContext(ctx).
		Where("usid = ?", usid).
		Order("created_at ASC").
		Find(&list).Error
	return list, err
}

func (r *permissionRepository) GetKBAcl(ctx context.Context, kbID, usid string) (*types.KBAcl, error) {
	var rec types.KBAcl
	err := r.db.WithContext(ctx).
		Where("kb_id = ? AND usid = ?", kbID, usid).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (r *permissionRepository) ListKBIDsByUsid(ctx context.Context, usid string) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).
		Model(&types.KBAcl{}).
		Where("usid = ?", usid).
		Pluck("kb_id", &ids).Error
	return ids, err
}

// ReplaceKBAcl atomically replaces the entire roster of kbID with the given
// (usid -> role) map: it upserts every entry in roles and deletes any existing
// member of kbID whose usid is NOT in roles. The whole operation runs in one
// transaction so a KB is never left half-updated. An empty roles map would
// delete every member; callers (the service) must guard against that.
func (r *permissionRepository) ReplaceKBAcl(ctx context.Context, kbID string, roles map[string]string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1) Upsert every target member.
		for usid, role := range roles {
			rec := &types.KBAcl{KBID: kbID, Usid: usid, Role: role}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "kb_id"}, {Name: "usid"}},
				DoUpdates: clause.AssignmentColumns([]string{"role", "updated_at"}),
			}).Create(rec).Error; err != nil {
				return err
			}
		}
		// 2) Delete members of this KB that are not in the target set.
		keep := make([]string, 0, len(roles))
		for usid := range roles {
			keep = append(keep, usid)
		}
		del := tx.Where("kb_id = ?", kbID)
		if len(keep) > 0 {
			del = del.Where("usid NOT IN ?", keep)
		}
		return del.Delete(&types.KBAcl{}).Error
	})
}

// ---- kb_acl_display (display-only roster source) ----

// UpsertKBAclDisplay stores or replaces the display source JSON for a KB.
func (r *permissionRepository) UpsertKBAclDisplay(ctx context.Context, kbID string, sourceJSON string) error {
	rec := &types.KBAclDisplay{KBID: kbID, Source: sourceJSON}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "kb_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"source", "updated_at"}),
		}).
		Create(rec).Error
}

// GetKBAclDisplay returns the stored display source JSON for a KB, or "" when
// none was ever saved (not an error).
func (r *permissionRepository) GetKBAclDisplay(ctx context.Context, kbID string) (string, error) {
	var rec types.KBAclDisplay
	err := r.db.WithContext(ctx).
		Where("kb_id = ?", kbID).
		First(&rec).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return rec.Source, nil
}

// ---- super_user ----

func (r *permissionRepository) IsSuperUser(ctx context.Context, usid string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&types.SuperUser{}).
		Where("usid = ?", usid).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// AddSuperUser is idempotent: re-adding an existing super_user is a no-op
// (keeps the original note/created_at).
func (r *permissionRepository) AddSuperUser(ctx context.Context, usid, note string) error {
	rec := &types.SuperUser{Usid: usid, Note: note}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(rec).Error
}

func (r *permissionRepository) RemoveSuperUser(ctx context.Context, usid string) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("usid = ?", usid).
		Delete(&types.SuperUser{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *permissionRepository) ListSuperUsers(ctx context.Context) ([]*types.SuperUser, error) {
	var list []*types.SuperUser
	err := r.db.WithContext(ctx).Order("created_at ASC").Find(&list).Error
	return list, err
}

// ---- common_kb ----

func (r *permissionRepository) ListCommonKBIDs(ctx context.Context) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).
		Model(&types.CommonKB{}).
		Pluck("kb_id", &ids).Error
	return ids, err
}

func (r *permissionRepository) ListCommonKBs(ctx context.Context) ([]*types.CommonKB, error) {
	var list []*types.CommonKB
	err := r.db.WithContext(ctx).Order("created_at ASC").Find(&list).Error
	return list, err
}

// AddCommonKB is idempotent: re-marking an existing common KB is a no-op.
func (r *permissionRepository) AddCommonKB(ctx context.Context, kbID, note string) error {
	rec := &types.CommonKB{KBID: kbID, Note: note}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(rec).Error
}

func (r *permissionRepository) RemoveCommonKB(ctx context.Context, kbID string) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("kb_id = ?", kbID).
		Delete(&types.CommonKB{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
