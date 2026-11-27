package sqlite

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/kipit-labs/kipitiny/internal/domain"
	"github.com/kipit-labs/kipitiny/internal/ids"
)

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	n, err := s.db.NewSelect().Model((*domain.User)(nil)).Count(ctx)
	return n, mapErr(err)
}

func (s *Store) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	return createUser(ctx, s.db, u)
}

func createUser(ctx context.Context, db bun.IDB, u domain.User) (domain.User, error) {
	u.ID, u.CreatedAt = ids.New(), now()
	if u.Role == "" {
		u.Role = domain.RoleMember
	}
	if u.Features == nil {
		u.Features = []string{}
	}
	if _, err := db.NewInsert().Model(&u).Exec(ctx); err != nil {
		return domain.User{}, mapErr(err)
	}
	return u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	us := []domain.User{}
	err := s.db.NewSelect().Model(&us).Order("created_at", "id").Scan(ctx)
	return us, mapErr(err)
}

func (s *Store) UpdateUserAccess(ctx context.Context, id string, role domain.Role, features []string) (domain.User, error) {
	if features == nil {
		features = []string{}
	}
	u := domain.User{ID: id, Role: role, Features: features}
	res, err := s.db.NewUpdate().Model(&u).Column("role", "features").WherePK().Exec(ctx)
	if err != nil {
		return domain.User{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.User{}, domain.ErrNotFound
	}
	return s.GetUser(ctx, id)
}

func (s *Store) SetUserPreferences(ctx context.Context, id string, p domain.Preferences) error {
	res, err := s.db.NewUpdate().Model(&domain.User{ID: id, Preferences: p}).Column("preferences").WherePK().Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "users", id)
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.NewDelete().Model((*domain.Session)(nil)).Where("user_id = ?", userID).Exec(ctx)
	return mapErr(err)
}

func (s *Store) ListGrants(ctx context.Context, userID string) ([]domain.ProjectGrant, error) {
	gs := []domain.ProjectGrant{}
	err := s.db.NewSelect().Model(&gs).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return gs, mapErr(err)
}

func (s *Store) SetGrant(ctx context.Context, g domain.ProjectGrant) error {
	return setGrant(ctx, s.db, g)
}

func setGrant(ctx context.Context, db bun.IDB, g domain.ProjectGrant) error {
	if len(g.Perms) == 0 {
		_, err := db.NewDelete().Model((*domain.ProjectGrant)(nil)).
			Where("user_id = ? AND project_id = ?", g.UserID, g.ProjectID).Exec(ctx)
		return mapErr(err)
	}
	g.CreatedAt = now()
	_, err := db.NewInsert().Model(&g).On("CONFLICT (user_id, project_id) DO UPDATE").Set("perms = EXCLUDED.perms").Exec(ctx)
	return mapErr(err)
}

func (s *Store) CreateInvite(ctx context.Context, i domain.Invite) (domain.Invite, error) {
	i.ID, i.CreatedAt, i.UsedAt = ids.New(), now(), nil
	if i.Features == nil {
		i.Features = []string{}
	}
	if i.Grants == nil {
		i.Grants = map[string][]string{}
	}
	if _, err := s.db.NewInsert().Model(&i).Exec(ctx); err != nil {
		return domain.Invite{}, mapErr(err)
	}
	return i, nil
}

func (s *Store) ListInvites(ctx context.Context) ([]domain.Invite, error) {
	is := []domain.Invite{}
	err := s.db.NewSelect().Model(&is).Where("used_at IS NULL AND expires_at > ?", now()).Order("created_at DESC").Scan(ctx)
	return is, mapErr(err)
}

func (s *Store) GetInviteByHash(ctx context.Context, hash string) (domain.Invite, error) {
	var i domain.Invite
	err := s.db.NewSelect().Model(&i).Where("token_hash = ? AND used_at IS NULL AND expires_at > ?", hash, now()).Scan(ctx)
	return i, mapErr(err)
}

func (s *Store) AcceptInvite(ctx context.Context, inviteID string, u domain.User, sso *domain.SSOIdentity) (domain.User, error) {
	var created domain.User
	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var i domain.Invite
		res, err := tx.NewUpdate().Model(&i).Set("used_at = ?", now()).
			Where("id = ? AND used_at IS NULL AND expires_at > ?", inviteID, now()).Returning("*").Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		u.Role, u.Features = i.Role, i.Features
		if created, err = createUser(ctx, tx, u); err != nil {
			return err
		}
		if sso != nil {
			id := *sso
			id.ID, id.UserID, id.CreatedAt = ids.New(), created.ID, now()
			if _, err := tx.NewInsert().Model(&id).Exec(ctx); err != nil {
				return mapErr(err)
			}
		}
		for projectID, perms := range i.Grants {
			// A project deleted since the invite was made is skipped.
			if exists, err := tx.NewSelect().Table("projects").Where("id = ?", projectID).Exists(ctx); err != nil {
				return mapErr(err)
			} else if !exists {
				continue
			}
			if err := setGrant(ctx, tx, domain.ProjectGrant{UserID: created.ID, ProjectID: projectID, Perms: perms}); err != nil {
				return err
			}
		}
		return nil
	})
	return created, err
}

func (s *Store) DeleteInvite(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "invites", id)
}

func (s *Store) GetUser(ctx context.Context, id string) (domain.User, error) {
	var u domain.User
	err := s.db.NewSelect().Model(&u).Where("id = ?", id).Scan(ctx)
	return u, mapErr(err)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (domain.User, error) {
	var u domain.User
	err := s.db.NewSelect().Model(&u).Where("username = ?", username).Scan(ctx)
	return u, mapErr(err)
}

func (s *Store) SetPassword(ctx context.Context, userID, passwordHash string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*domain.User)(nil)).
			Set("password_hash = ?", passwordHash).Where("id = ?", userID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.NewDelete().Model((*domain.Session)(nil)).Where("user_id = ?", userID).Exec(ctx)
		return mapErr(err)
	})
}

func (s *Store) SetTOTP(ctx context.Context, userID, secret string, codeHashes []string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewUpdate().Model((*domain.User)(nil)).
			Set("totp_secret = ?", secret).Set("totp_last_step = 0").Where("id = ?", userID).Exec(ctx)
		if err != nil {
			return mapErr(err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.ErrNotFound
		}
		return replaceRecoveryCodes(ctx, tx, userID, codeHashes)
	})
}

func (s *Store) UseTOTPStep(ctx context.Context, userID string, step int64) error {
	res, err := s.db.NewUpdate().Model((*domain.User)(nil)).Set("totp_last_step = ?", step).
		Where("id = ?", userID).Where("totp_last_step < ?", step).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrConflict
	}
	return nil
}

func (s *Store) SetRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return replaceRecoveryCodes(ctx, tx, userID, codeHashes)
	})
}

type recoveryCode struct {
	bun.BaseModel `bun:"table:recovery_codes"`

	CodeHash  string    `bun:"code_hash,pk"`
	UserID    string    `bun:"user_id"`
	CreatedAt time.Time `bun:"created_at"`
}

func replaceRecoveryCodes(ctx context.Context, tx bun.Tx, userID string, codeHashes []string) error {
	if _, err := tx.NewDelete().Model((*recoveryCode)(nil)).Where("user_id = ?", userID).Exec(ctx); err != nil {
		return mapErr(err)
	}
	if len(codeHashes) == 0 {
		return nil
	}
	rows := make([]recoveryCode, len(codeHashes))
	for i, h := range codeHashes {
		rows[i] = recoveryCode{CodeHash: h, UserID: userID, CreatedAt: now()}
	}
	_, err := tx.NewInsert().Model(&rows).Exec(ctx)
	return mapErr(err)
}

func (s *Store) CountRecoveryCodes(ctx context.Context, userID string) (int, error) {
	n, err := s.db.NewSelect().Model((*recoveryCode)(nil)).Where("user_id = ?", userID).Count(ctx)
	return n, mapErr(err)
}

func (s *Store) UseRecoveryCode(ctx context.Context, userID, codeHash string) error {
	res, err := s.db.NewDelete().Model((*recoveryCode)(nil)).
		Where("user_id = ?", userID).Where("code_hash = ?", codeHash).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) CreatePasskey(ctx context.Context, p domain.Passkey) (domain.Passkey, error) {
	p.ID, p.CreatedAt = ids.New(), now()
	if _, err := s.db.NewInsert().Model(&p).Exec(ctx); err != nil {
		return domain.Passkey{}, mapErr(err)
	}
	return p, nil
}

func (s *Store) ListPasskeys(ctx context.Context, userID string) ([]domain.Passkey, error) {
	ps := []domain.Passkey{}
	err := s.db.NewSelect().Model(&ps).Where("user_id = ?", userID).Order("created_at").Scan(ctx)
	return ps, mapErr(err)
}

func (s *Store) GetPasskeyByCredentialID(ctx context.Context, credentialID string) (domain.Passkey, error) {
	var p domain.Passkey
	err := s.db.NewSelect().Model(&p).Where("credential_id = ?", credentialID).Scan(ctx)
	return p, mapErr(err)
}

func (s *Store) PasskeyUsed(ctx context.Context, id, credential string) error {
	_, err := s.db.NewUpdate().Model((*domain.Passkey)(nil)).
		Set("credential = ?", credential).Set("last_used_at = ?", now()).Where("id = ?", id).Exec(ctx)
	return mapErr(err)
}

func (s *Store) DeletePasskey(ctx context.Context, userID, id string) error {
	res, err := s.db.NewDelete().Model((*domain.Passkey)(nil)).Where("id = ?", id).Where("user_id = ?", userID).Exec(ctx)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
