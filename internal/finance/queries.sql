-- name: InsertAccount :exec
INSERT INTO financial_accounts(id,space_id,name,kind,currency,scale,responsible_member,visibility,space_permission,institution,last_digits,tracking_date,opening_minor,balance_minor,quality,counterparty_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$13,$14,$15);

-- name: Account :one
SELECT sqlc.embed(a), (rm.ended_at IS NULL AND rm.role<>'reader')::boolean AS responsible_active,
 (a.responsible_member=sqlc.arg(member_id) OR (a.visibility='space' AND a.space_permission='operate')
 OR (a.visibility='selected' AND EXISTS (SELECT 1 FROM account_grants g WHERE g.space_id=a.space_id AND g.account_id=a.id AND g.member_id=sqlc.arg(member_id) AND g.permission='operate')))::boolean AS granted_operate,
 EXISTS(SELECT 1 FROM space_capabilities c WHERE c.space_id=a.space_id AND c.capability='finance.write' AND c.ceiling>0 AND c.used<=c.ceiling)::boolean AS write_available
FROM financial_accounts a JOIN memberships rm ON rm.space_id=a.space_id AND rm.id=a.responsible_member
WHERE a.space_id=sqlc.arg(space_id) AND a.id=sqlc.arg(id)
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space'
 OR (a.visibility='selected' AND EXISTS (SELECT 1 FROM account_grants g
 WHERE g.space_id=a.space_id AND g.account_id=a.id AND g.member_id=sqlc.arg(member_id))))
FOR UPDATE OF a;

-- name: Accounts :many
SELECT sqlc.embed(a), (rm.ended_at IS NULL AND rm.role<>'reader')::boolean AS responsible_active,
 (a.responsible_member=sqlc.arg(member_id) OR (a.visibility='space' AND a.space_permission='operate')
 OR (a.visibility='selected' AND EXISTS (SELECT 1 FROM account_grants g WHERE g.space_id=a.space_id AND g.account_id=a.id AND g.member_id=sqlc.arg(member_id) AND g.permission='operate')))::boolean AS granted_operate,
 EXISTS(SELECT 1 FROM space_capabilities c WHERE c.space_id=a.space_id AND c.capability='finance.write' AND c.ceiling>0 AND c.used<=c.ceiling)::boolean AS write_available
FROM financial_accounts a JOIN memberships rm ON rm.space_id=a.space_id AND rm.id=a.responsible_member
WHERE a.space_id=sqlc.arg(space_id) AND a.id>sqlc.arg(cursor)
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space'
 OR (a.visibility='selected' AND EXISTS (SELECT 1 FROM account_grants g
 WHERE g.space_id=a.space_id AND g.account_id=a.id AND g.member_id=sqlc.arg(member_id))))
ORDER BY a.id LIMIT sqlc.arg(page_size);

-- name: AccountGrants :many
SELECT g.* FROM account_grants g JOIN memberships m ON m.space_id=g.space_id AND m.id=g.member_id
WHERE g.space_id=$1 AND g.account_id=$2 AND m.ended_at IS NULL ORDER BY g.member_id;

-- name: GrantMembers :many
SELECT id,role FROM memberships WHERE space_id=$1 AND id=ANY(sqlc.arg(member_ids)::text[]) AND ended_at IS NULL;

-- name: HasOtherMembers :one
SELECT EXISTS(SELECT 1 FROM memberships WHERE space_id=$1 AND id<>$2 AND ended_at IS NULL);

-- name: PutAccountGrants :exec
INSERT INTO account_grants(space_id,account_id,member_id,permission)
SELECT sqlc.arg(space_id)::text,sqlc.arg(account_id)::text,
unnest(sqlc.arg(member_ids)::text[]),unnest(sqlc.arg(permissions)::text[]);

-- name: ClearAccountGrants :exec
DELETE FROM account_grants WHERE space_id=$1 AND account_id=$2;

-- name: CaptureAccountConfiguration :exec
INSERT INTO account_configuration_history(space_id,account_id,version,name,institution,last_digits,responsible_member,visibility,space_permission,state,counterparty_id,actor)
SELECT a.space_id,a.id,a.version,a.name,a.institution,a.last_digits,a.responsible_member,a.visibility,a.space_permission,a.state,a.counterparty_id,sqlc.arg(actor)
FROM financial_accounts a WHERE a.space_id=sqlc.arg(space_id) AND a.id=sqlc.arg(id);

-- name: CaptureAccountGrants :exec
INSERT INTO account_grant_history(space_id,account_id,version,member_id,permission)
SELECT g.space_id,g.account_id,a.version,g.member_id,g.permission
FROM account_grants g JOIN financial_accounts a ON a.space_id=g.space_id AND a.id=g.account_id
WHERE g.space_id=$1 AND g.account_id=$2;

-- name: EditAccount :execrows
UPDATE financial_accounts SET name=$4,institution=$5,last_digits=$6,state=$7,counterparty_id=$8,version=version+1
WHERE space_id=$1 AND id=$2 AND version=$3;

-- name: ShareAccount :execrows
UPDATE financial_accounts SET visibility=$4,space_permission=$5,version=version+1
WHERE space_id=$1 AND id=$2 AND version=$3;

-- name: TransferAccount :execrows
UPDATE financial_accounts SET responsible_member=$4,version=version+1
WHERE space_id=$1 AND id=$2 AND version=$3;

-- name: RecoverableAccount :one
SELECT a.* FROM financial_accounts a JOIN memberships m ON m.id=a.responsible_member AND m.space_id=a.space_id
WHERE a.space_id=$1 AND a.id=$2 AND m.principal_id=$3 AND m.ended_at IS NOT NULL
FOR UPDATE OF a;

-- name: RecoverableAccounts :many
SELECT a.id,a.version FROM financial_accounts a JOIN memberships m ON m.id=a.responsible_member AND m.space_id=a.space_id
WHERE a.space_id=$1 AND m.principal_id=$2 AND m.ended_at IS NOT NULL AND a.id>$3 ORDER BY a.id LIMIT $4;

-- name: InsertCatalog :exec
INSERT INTO financial_catalog(id,space_id,kind,name,direction,parent_id,icon,color,default_key)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: CatalogEntry :one
SELECT * FROM financial_catalog WHERE space_id=$1 AND id=$2 FOR UPDATE;

-- name: CatalogEntries :many
SELECT * FROM financial_catalog WHERE space_id=$1 AND kind=$2 AND id>$3 ORDER BY id LIMIT $4;

-- name: CatalogChildren :one
SELECT count(*) FROM financial_catalog WHERE space_id=$1 AND parent_id=$2 AND merged_into IS NULL;

-- name: CatalogActiveChildren :one
SELECT count(*) FROM financial_catalog WHERE space_id=$1 AND parent_id=$2 AND NOT archived;

-- name: CaptureCatalogConfiguration :exec
INSERT INTO catalog_configuration_history(space_id,entry_id,version,name,parent_id,icon,color,archived,merged_into,actor)
SELECT c.space_id,c.id,c.version,c.name,c.parent_id,c.icon,c.color,c.archived,c.merged_into,sqlc.arg(actor)
FROM financial_catalog c WHERE c.space_id=sqlc.arg(space_id) AND c.id=sqlc.arg(id);

-- name: CaptureCatalogMergeConfiguration :exec
INSERT INTO catalog_configuration_history(space_id,entry_id,version,name,parent_id,icon,color,archived,merged_into,actor)
SELECT c.space_id,c.id,c.version,c.name,c.parent_id,c.icon,c.color,c.archived,c.merged_into,sqlc.arg(actor)
FROM financial_catalog c WHERE c.space_id=sqlc.arg(space_id) AND (c.id=sqlc.arg(id) OR c.parent_id=sqlc.arg(id) OR c.merged_into=sqlc.arg(id));

-- name: EditCatalog :execrows
UPDATE financial_catalog SET name=$4,parent_id=$5,icon=$6,color=$7,archived=$8,version=version+1
WHERE space_id=$1 AND id=$2 AND version=$3 AND merged_into IS NULL;

-- name: MergeCatalog :execrows
UPDATE financial_catalog SET merged_into=$4,archived=true,version=version+1
WHERE space_id=$1 AND id=$2 AND version=$3 AND merged_into IS NULL;

-- name: RepointCatalogAliases :exec
UPDATE financial_catalog SET merged_into=$3,version=version+1 WHERE space_id=$1 AND merged_into=$2;

-- name: ReparentCategories :exec
UPDATE financial_catalog SET parent_id=$3,version=version+1 WHERE space_id=$1 AND parent_id=$2;

-- name: RecordCatalogMerge :exec
INSERT INTO catalog_merges(space_id,source_id,target_id,actor) VALUES ($1,$2,$3,$4);

-- name: SeedCatalog :exec
INSERT INTO financial_catalog(id,space_id,kind,name,direction,icon,color,default_key)
VALUES ($1,$2,'category',$3,$4,$5,$6,$7) ON CONFLICT(space_id,default_key) DO NOTHING;

-- name: InsertExchangeReference :exec
INSERT INTO exchange_references(id,space_id,from_currency,to_currency,rate,effective_date,context,source,precision_digits,replaces,author)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11);

-- name: ExchangeReference :one
SELECT * FROM exchange_references WHERE space_id=$1 AND id=$2;

-- name: ExchangeReferences :many
SELECT * FROM exchange_references WHERE space_id=sqlc.arg(space_id) AND id>sqlc.arg(cursor)
AND (sqlc.arg(from_currency)::text='' OR from_currency=sqlc.arg(from_currency))
AND (sqlc.arg(to_currency)::text='' OR to_currency=sqlc.arg(to_currency))
AND (sqlc.arg(context)::text='' OR context=sqlc.arg(context))
ORDER BY id LIMIT sqlc.arg(page_size);
