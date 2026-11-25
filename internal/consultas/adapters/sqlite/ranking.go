package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hectorruiz23/la-mordida/internal/consultas/domain"
	"github.com/hectorruiz23/la-mordida/internal/kernel"
)

// Ranking counts the materialised award units inside one scope. Numerator,
// denominator and detail share the same WHERE clause, so the percentage is
// always a share of the awards the list can justify.
func (s *Store) Ranking(ctx context.Context, query domain.RankingQuery) (domain.Ranking, error) {
	where, args, buyer, err := s.scope(ctx, query)
	if err != nil {
		return domain.Ranking{}, err
	}

	ranking := domain.Ranking{
		Buyer:   buyer,
		From:    query.From,
		To:      query.To,
		Offset:  query.Offset,
		Limit:   query.Limit,
		Ranking: []domain.WinnerRank{},
	}
	if err := s.database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM award_unit u WHERE `+where, args...,
	).Scan(&ranking.Total); err != nil {
		return domain.Ranking{}, fmt.Errorf("count award units: %w", err)
	}

	pageArgs := append(append([]any{}, args...), query.Limit, query.Offset)
	rows, err := s.database.QueryContext(ctx, `
		SELECT
			CASE
				WHEN u.winner_id = '' THEN 'unit:' || u.unit_key
				WHEN e.id IS NOT NULL THEN 'entity:' || e.id
				ELSE 'party:' || u.winner_scheme || '|' || u.winner_id
			END AS entity_key,
			COALESCE(MAX(e.id), 0),
			MAX(u.winner_name),
			COUNT(*) AS wins
		FROM award_unit u
		LEFT JOIN entity_identity ei
		       ON u.winner_id <> '' AND ei.scheme = u.winner_scheme AND ei.external_id = u.winner_id
		LEFT JOIN entity e ON e.id = ei.entity_id
		WHERE `+where+`
		GROUP BY entity_key
		ORDER BY wins DESC, entity_key ASC
		LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return domain.Ranking{}, fmt.Errorf("read ranking: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, publishedName string
		var entityID int64
		var wins int
		if err := rows.Scan(&key, &entityID, &publishedName, &wins); err != nil {
			return domain.Ranking{}, fmt.Errorf("scan ranking: %w", err)
		}

		entity, err := s.rankedEntity(ctx, key, entityID, publishedName)
		if err != nil {
			return domain.Ranking{}, err
		}
		ranking.Ranking = append(ranking.Ranking, domain.WinnerRank{
			Entity:     entity,
			Wins:       wins,
			Percentage: domain.Percentage(wins, ranking.Total),
		})
	}
	if err := rows.Err(); err != nil {
		return domain.Ranking{}, fmt.Errorf("iterate ranking: %w", err)
	}
	return ranking, nil
}

// Awards reads the justifying list of one entity under the same scope. The
// entity is never joined to its members here: a UTE's award is one line, not
// one line per member.
func (s *Store) Awards(ctx context.Context, query domain.RankingQuery, entityKey string) (domain.AwardPage, error) {
	where, args, _, err := s.scope(ctx, query)
	if err != nil {
		return domain.AwardPage{}, err
	}
	entityWhere, entityArgs, err := entityPredicate(entityKey)
	if err != nil {
		return domain.AwardPage{}, err
	}
	scopeWhere := where + " AND " + entityWhere
	scopeArgs := append(append([]any{}, args...), entityArgs...)

	page := domain.AwardPage{
		Entity: domain.Entity{EntityRef: bareEntity(entityKey)},
		Offset: query.Offset,
		Limit:  query.Limit,
		Awards: []domain.AwardLine{},
	}
	if kind, entityID, _, _, _, err := domain.ParseEntityKey(entityKey); err == nil && kind == "entity" {
		if page.Entity, err = s.entityWithRelations(ctx, entityID); err != nil {
			return domain.AwardPage{}, err
		}
	}
	if err := s.database.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM award_unit u WHERE `+scopeWhere, scopeArgs...,
	).Scan(&page.Total); err != nil {
		return domain.AwardPage{}, fmt.Errorf("count entity awards: %w", err)
	}

	pageArgs := append(append([]any{}, scopeArgs...), query.Limit, query.Offset)
	rows, err := s.database.QueryContext(ctx, `
		SELECT u.unit_key, u.expediente_id, e.folder_id, e.object,
		       e.buyer_scheme, e.buyer_id, e.buyer_name,
		       u.lot_id, COALESCE(l.object, ''), COALESCE(u.award_date, ''),
		       u.result_code, u.winner_scheme, u.winner_id, u.winner_name,
		       u.contract_id, COALESCE(u.contract_issue_date, ''), u.state,
		       u.publication_id, p.source, p.external_id, p.url,
		       p.published_at, COALESCE(p.effective_date, '')
		FROM award_unit u
		JOIN expediente e ON e.id = u.expediente_id
		JOIN publication p ON p.id = u.publication_id
		LEFT JOIN lot l ON l.expediente_id = u.expediente_id AND l.lot_id = u.lot_id
		WHERE `+scopeWhere+`
		ORDER BY COALESCE(u.award_date, '') ASC, u.unit_key ASC
		LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return domain.AwardPage{}, fmt.Errorf("read entity awards: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var line domain.AwardLine
		var expedienteID, publicationID int64
		var contractID, contractIssueDate string
		if err := rows.Scan(
			&line.UnitKey, &expedienteID, &line.FolderID, &line.Object,
			&line.Buyer.Scheme, &line.Buyer.ID, &line.Buyer.Name,
			&line.LotID, &line.LotObject, &line.AwardDate,
			&line.ResultCode, &line.Winner.Scheme, &line.Winner.ID, &line.Winner.Name,
			&contractID, &contractIssueDate, &line.State,
			&publicationID, &line.Publication.Source, &line.Publication.ExternalID,
			&line.Publication.URL, &line.Publication.UpdatedAt, &line.Publication.EffectiveDate,
		); err != nil {
			return domain.AwardPage{}, fmt.Errorf("scan entity award: %w", err)
		}
		if contractID != "" || contractIssueDate != "" {
			line.Contract = &domain.Contract{ID: contractID, IssueDate: contractIssueDate}
		}
		if line.Amounts, err = s.awardUnitAmounts(ctx, expedienteID, line.LotID); err != nil {
			return domain.AwardPage{}, err
		}
		if line.Documents, err = documents(ctx, s.database, publicationID); err != nil {
			return domain.AwardPage{}, err
		}
		page.Awards = append(page.Awards, line)
	}
	if err := rows.Err(); err != nil {
		return domain.AwardPage{}, fmt.Errorf("iterate entity awards: %w", err)
	}

	if page.Entity.Name == "" {
		for _, line := range page.Awards {
			if line.Winner.Name != "" {
				page.Entity.Name = line.Winner.Name
				page.Entity.Names = []string{line.Winner.Name}
				break
			}
		}
	}
	return page, nil
}

func (s *Store) rankedEntity(ctx context.Context, key string, entityID int64, publishedName string) (domain.Entity, error) {
	if entityID > 0 {
		entity, err := s.entityWithRelations(ctx, entityID)
		if err != nil {
			return domain.Entity{}, err
		}
		if entity.Name == "" && publishedName != "" {
			entity.Name = publishedName
			entity.Names = appendName(entity.Names, publishedName)
		}
		return entity, nil
	}
	entity := domain.Entity{EntityRef: bareEntity(key)}
	if publishedName != "" {
		entity.Name = publishedName
		entity.Names = []string{publishedName}
	}
	return entity, nil
}

func appendName(names []string, name string) []string {
	for _, existing := range names {
		if existing == name {
			return names
		}
	}
	return append(names, name)
}

func bareEntity(key string) domain.EntityRef {
	kind, _, scheme, id, _, err := domain.ParseEntityKey(key)
	if err != nil {
		return domain.EntityRef{Key: key}
	}
	entity := domain.EntityRef{Key: key}
	switch kind {
	case "party":
		entity.Scheme = scheme
		entity.ID = id
	case "unit":
		entity.Kind = "unidentified"
	}
	return entity
}

func (s *Store) entityWithRelations(ctx context.Context, id int64) (domain.Entity, error) {
	ref, err := s.entityRef(ctx, id)
	if err != nil {
		return domain.Entity{}, err
	}
	entity := domain.Entity{EntityRef: ref}
	if entity.Members, err = s.entityMembers(ctx, id); err != nil {
		return domain.Entity{}, err
	}
	if entity.Groups, err = s.entityGroups(ctx, id); err != nil {
		return domain.Entity{}, err
	}
	return entity, nil
}

func (s *Store) entityRef(ctx context.Context, id int64) (domain.EntityRef, error) {
	var kind, legalName string
	err := s.database.QueryRowContext(ctx,
		`SELECT kind, legal_name FROM entity WHERE id = ?`, id,
	).Scan(&kind, &legalName)
	if err != nil {
		return domain.EntityRef{}, fmt.Errorf("read entity %d: %w", id, err)
	}

	ref := domain.EntityRef{Key: domain.EntityKey(id), Kind: kind, Name: legalName}
	rows, err := s.database.QueryContext(ctx,
		`SELECT scheme, external_id FROM entity_identity WHERE entity_id = ? ORDER BY scheme, external_id`, id)
	if err != nil {
		return domain.EntityRef{}, fmt.Errorf("read entity identities: %w", err)
	}
	for rows.Next() {
		var party domain.Party
		if err := rows.Scan(&party.Scheme, &party.ID); err != nil {
			rows.Close()
			return domain.EntityRef{}, fmt.Errorf("scan entity identity: %w", err)
		}
		ref.Identities = append(ref.Identities, party)
		if ref.Scheme == "" {
			ref.Scheme = party.Scheme
			ref.ID = party.ID
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.EntityRef{}, fmt.Errorf("iterate entity identities: %w", err)
	}
	rows.Close()

	rows, err = s.database.QueryContext(ctx,
		`SELECT name FROM entity_name WHERE entity_id = ? ORDER BY name`, id)
	if err != nil {
		return domain.EntityRef{}, fmt.Errorf("read entity names: %w", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return domain.EntityRef{}, fmt.Errorf("scan entity name: %w", err)
		}
		ref.Names = append(ref.Names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return domain.EntityRef{}, fmt.Errorf("iterate entity names: %w", err)
	}
	rows.Close()

	if ref.Name == "" && len(ref.Names) > 0 {
		ref.Name = ref.Names[0]
	}
	if len(ref.Names) == 0 && ref.Name != "" {
		ref.Names = []string{ref.Name}
	}
	return ref, nil
}

func (s *Store) entityMembers(ctx context.Context, groupID int64) ([]domain.Membership, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT member_id, source, valid_from, valid_to, percentage_bps
		FROM entity_member
		WHERE group_id = ?
		ORDER BY valid_from, member_id, source`, groupID)
	if err != nil {
		return nil, fmt.Errorf("read entity members: %w", err)
	}
	defer rows.Close()

	var members []domain.Membership
	for rows.Next() {
		var memberID int64
		var membership domain.Membership
		var percentage sql.NullInt64
		if err := rows.Scan(&memberID, &membership.Source, &membership.ValidFrom, &membership.ValidTo, &percentage); err != nil {
			return nil, fmt.Errorf("scan entity member: %w", err)
		}
		member, err := s.entityRef(ctx, memberID)
		if err != nil {
			return nil, err
		}
		membership.Member = member
		if percentage.Valid {
			membership.Percentage = basisPoints(percentage.Int64)
		}
		members = append(members, membership)
	}
	return members, rows.Err()
}

func (s *Store) entityGroups(ctx context.Context, entityID int64) ([]domain.GroupLink, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT group_id, relation, source, valid_from, valid_to
		FROM entity_group
		WHERE entity_id = ?
		ORDER BY relation, valid_from, group_id`, entityID)
	if err != nil {
		return nil, fmt.Errorf("read entity groups: %w", err)
	}
	defer rows.Close()

	var groups []domain.GroupLink
	for rows.Next() {
		var groupID int64
		var link domain.GroupLink
		if err := rows.Scan(&groupID, &link.Relation, &link.Source, &link.ValidFrom, &link.ValidTo); err != nil {
			return nil, fmt.Errorf("scan entity group: %w", err)
		}
		group, err := s.entityRef(ctx, groupID)
		if err != nil {
			return nil, err
		}
		link.Group = group
		groups = append(groups, link)
	}
	return groups, rows.Err()
}

func basisPoints(value int64) string {
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

// scope builds the shared WHERE clause. A buyer filter is resolved through its
// accredited identity, never through a folder id; an unknown buyer is an
// error, not an empty ranking.
func (s *Store) scope(ctx context.Context, query domain.RankingQuery) (string, []any, *domain.Party, error) {
	clauses := []string{"u.state = 'known'"}
	args := []any{}
	var buyer *domain.Party

	if query.BuyerScheme != "" {
		var buyerID int64
		var name string
		err := s.database.QueryRowContext(ctx, `
			SELECT b.id, b.name
			FROM buyer b
			JOIN buyer_identity bi ON bi.buyer_id = b.id
			WHERE bi.scheme = ? AND bi.external_id = ?`,
			query.BuyerScheme, query.BuyerID,
		).Scan(&buyerID, &name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return "", nil, nil, domain.ErrUnknownBuyer
		case err != nil:
			return "", nil, nil, fmt.Errorf("resolve buyer: %w", err)
		}
		clauses = append(clauses, "u.buyer_ref = ?")
		args = append(args, buyerID)
		buyer = &domain.Party{Scheme: query.BuyerScheme, ID: query.BuyerID, Name: name}
	}

	if query.From != "" {
		clauses = append(clauses, "u.award_date IS NOT NULL", "u.award_date >= ?")
		args = append(args, query.From)
	}
	if query.To != "" {
		clauses = append(clauses, "u.award_date IS NOT NULL", "u.award_date <= ?")
		args = append(args, query.To)
	}
	return strings.Join(clauses, " AND "), args, buyer, nil
}

func entityPredicate(key string) (string, []any, error) {
	kind, entityID, scheme, id, unitKey, err := domain.ParseEntityKey(key)
	if err != nil {
		return "", nil, err
	}
	switch kind {
	case "entity":
		return `u.winner_id <> '' AND EXISTS (
			SELECT 1 FROM entity_identity ei
			WHERE ei.entity_id = ? AND ei.scheme = u.winner_scheme AND ei.external_id = u.winner_id)`,
			[]any{entityID}, nil
	case "party":
		return `u.winner_scheme = ? AND u.winner_id = ?`, []any{scheme, id}, nil
	case "unit":
		return `u.unit_key = ?`, []any{unitKey}, nil
	default:
		return "", nil, domain.ErrInvalidEntityKey
	}
}

func (s *Store) awardUnitAmounts(ctx context.Context, expedienteID int64, lotID string) ([]kernel.Money, error) {
	rows, err := s.database.QueryContext(ctx, `
		SELECT tax_base, currency, amount_cents
		FROM award_projection_amount
		WHERE expediente_id = ? AND lot_id = ?
		ORDER BY tax_base`, expedienteID, lotID)
	if err != nil {
		return nil, fmt.Errorf("read award unit amounts: %w", err)
	}
	defer rows.Close()

	var amounts []kernel.Money
	for rows.Next() {
		var base, currency string
		var cents int64
		if err := rows.Scan(&base, &currency, &cents); err != nil {
			return nil, fmt.Errorf("scan award unit amount: %w", err)
		}
		amounts = append(amounts, kernel.Money{Cents: cents, Currency: currency, TaxBase: kernel.TaxBase(base)})
	}
	return amounts, rows.Err()
}
