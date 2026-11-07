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

-- name: CatalogReferences :many
SELECT c.* FROM financial_catalog c WHERE c.space_id=sqlc.arg(space_id)
AND (c.id=ANY(sqlc.arg(ids)::text[]) OR c.id IN
 (SELECT original.merged_into FROM financial_catalog original WHERE original.space_id=sqlc.arg(space_id) AND original.id=ANY(sqlc.arg(ids)::text[])));

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

-- name: InsertOperation :exec
INSERT INTO financial_operations(id,space_id,kind,author,origin) VALUES ($1,$2,$3,$4,'manual');

-- name: InsertOperationRevision :exec
INSERT INTO operation_revisions(space_id,operation_id,revision,effective_date,author,note,exceptional,merchant_id,counterparty_id,original_minor,original_currency,change_kind,reason,voided)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14);

-- name: InsertPositionEffect :exec
INSERT INTO position_effects(space_id,operation_id,revision,ordinal,account_id,kind,amount_minor,in_tracking)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: InsertEconomicComponent :exec
INSERT INTO economic_components(space_id,operation_id,revision,ordinal,effect_ordinal,direction,amount_minor,category_id,exchange_reference_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: InsertOperationTags :exec
INSERT INTO operation_tags(space_id,operation_id,revision,tag_id)
SELECT sqlc.arg(space_id)::text,sqlc.arg(operation_id)::text,sqlc.arg(revision)::bigint,unnest(sqlc.arg(tag_ids)::text[]);

-- name: SetAccountProjection :execrows
UPDATE financial_accounts SET balance_minor=$3 WHERE space_id=$1 AND id=$2;

-- name: Operation :one
SELECT sqlc.embed(o),sqlc.embed(r)
FROM financial_operations o JOIN operation_revisions r
 ON (r.space_id,r.operation_id)=(o.space_id,o.id)
 AND r.revision=CASE WHEN sqlc.arg(requested_revision)::bigint=0 THEN o.current_revision ELSE sqlc.arg(requested_revision) END
WHERE o.space_id=sqlc.arg(space_id) AND o.id=sqlc.arg(id)
AND NOT EXISTS(SELECT 1 FROM position_effects e JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
 WHERE (e.space_id,e.operation_id,e.revision)=(r.space_id,r.operation_id,r.revision)
 AND NOT (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id))))));

-- name: OperationEffects :many
SELECT sqlc.embed(e),a.currency,a.scale FROM position_effects e
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
WHERE e.space_id=$1 AND e.operation_id=$2 AND e.revision=$3 ORDER BY e.ordinal;

-- name: OperationComponents :many
SELECT c.*,COALESCE(k.merged_into,k.id,'')::text AS canonical_category_id
FROM economic_components c LEFT JOIN financial_catalog k ON (k.space_id,k.id)=(c.space_id,c.category_id)
WHERE c.space_id=$1 AND c.operation_id=$2 AND c.revision=$3 ORDER BY c.ordinal;

-- name: OperationTags :many
SELECT t.tag_id,COALESCE(k.merged_into,k.id)::text AS canonical_tag_id FROM operation_tags t
JOIN financial_catalog k ON (k.space_id,k.id)=(t.space_id,t.tag_id)
WHERE t.space_id=$1 AND t.operation_id=$2 AND t.revision=$3 ORDER BY t.tag_id;

-- name: AccountOperationHistory :many
SELECT o.id,o.kind,r.effective_date,e.ordinal,e.kind AS effect_kind,e.amount_minor,e.in_tracking
FROM position_effects e
JOIN financial_operations o ON (o.space_id,o.id,o.current_revision)=(e.space_id,e.operation_id,e.revision)
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(e.space_id,e.operation_id,e.revision)
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
WHERE e.space_id=sqlc.arg(space_id) AND e.account_id=sqlc.arg(account_id) AND NOT r.voided
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)))))
AND (o.id,e.ordinal)>(sqlc.arg(cursor)::text,sqlc.arg(cursor_ordinal)::integer)
ORDER BY o.id,e.ordinal LIMIT sqlc.arg(page_size);

-- name: ReconstructedBalance :one
SELECT COALESCE((a.opening_minor::numeric+COALESCE((SELECT sum(e.amount_minor) FROM position_effects e
JOIN financial_operations o ON (o.space_id,o.id,o.current_revision)=(e.space_id,e.operation_id,e.revision)
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(e.space_id,e.operation_id,e.revision)
WHERE e.space_id=a.space_id AND e.account_id=a.id AND e.in_tracking AND NOT r.voided),0))::text,'')::text AS amount_minor
FROM financial_accounts a WHERE a.space_id=$1 AND a.id=$2;

-- name: AdvanceOperationRevision :execrows
UPDATE financial_operations SET current_revision=current_revision+1
WHERE space_id=$1 AND id=$2 AND current_revision=$3;

-- name: CopyVoidedRevision :exec
WITH revision AS (
 INSERT INTO operation_revisions(space_id,operation_id,revision,effective_date,author,note,exceptional,merchant_id,counterparty_id,original_minor,original_currency,change_kind,reason,voided)
 SELECT r.space_id,r.operation_id,r.revision+1,r.effective_date,sqlc.arg(author),r.note,r.exceptional,r.merchant_id,r.counterparty_id,r.original_minor,r.original_currency,'void',sqlc.arg(reason),true
 FROM operation_revisions r WHERE r.space_id=sqlc.arg(space_id) AND r.operation_id=sqlc.arg(operation_id) AND r.revision=sqlc.arg(revision)
 RETURNING space_id,operation_id,revision
), effects AS (
 INSERT INTO position_effects(space_id,operation_id,revision,ordinal,account_id,kind,amount_minor,in_tracking)
 SELECT e.space_id,e.operation_id,n.revision,e.ordinal,e.account_id,e.kind,e.amount_minor,e.in_tracking
 FROM position_effects e JOIN revision n ON (n.space_id,n.operation_id,n.revision-1)=(e.space_id,e.operation_id,e.revision)
), components AS (
 INSERT INTO economic_components(space_id,operation_id,revision,ordinal,effect_ordinal,direction,amount_minor,category_id,exchange_reference_id)
 SELECT c.space_id,c.operation_id,n.revision,c.ordinal,c.effect_ordinal,c.direction,c.amount_minor,c.category_id,c.exchange_reference_id
 FROM economic_components c JOIN revision n ON (n.space_id,n.operation_id,n.revision-1)=(c.space_id,c.operation_id,c.revision)
)
INSERT INTO operation_tags(space_id,operation_id,revision,tag_id)
SELECT t.space_id,t.operation_id,n.revision,t.tag_id
FROM operation_tags t JOIN revision n ON (n.space_id,n.operation_id,n.revision-1)=(t.space_id,t.operation_id,t.revision);

-- name: OperationRevisions :many
SELECT r.* FROM operation_revisions r
WHERE r.space_id=sqlc.arg(space_id) AND r.operation_id=sqlc.arg(operation_id) AND r.revision>sqlc.arg(cursor)::bigint
AND NOT EXISTS(SELECT 1 FROM position_effects e JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
 WHERE (e.space_id,e.operation_id,e.revision)=(r.space_id,r.operation_id,r.revision)
 AND NOT (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id))))))
ORDER BY r.revision LIMIT sqlc.arg(page_size);

-- name: InsertRefundLink :exec
INSERT INTO operation_refunds(space_id,operation_id,revision,purchase_id,purchase_revision,purchase_minor)
VALUES($1,$2,$3,$4,$5,$6);

-- name: RefundLink :one
SELECT f.* FROM operation_refunds f
WHERE f.space_id=sqlc.arg(space_id) AND f.operation_id=sqlc.arg(operation_id) AND f.revision=sqlc.arg(revision)
AND NOT EXISTS(SELECT 1 FROM position_effects e JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
 WHERE (e.space_id,e.operation_id,e.revision)=(f.space_id,f.purchase_id,f.purchase_revision)
 AND NOT (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id))))));

-- name: RefundedPurchaseAmount :one
SELECT COALESCE(sum(f.purchase_minor),0)::text AS amount_minor,COALESCE(min(r.effective_date),'infinity'::date)::date AS earliest_date
FROM operation_refunds f
JOIN financial_operations o ON (o.space_id,o.id,o.current_revision)=(f.space_id,f.operation_id,f.revision)
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(f.space_id,f.operation_id,f.revision)
WHERE f.space_id=sqlc.arg(space_id) AND f.purchase_id=sqlc.arg(purchase_id)
AND f.operation_id<>sqlc.arg(exclude_id)::text AND NOT r.voided;

-- name: CopyVoidedRefundLink :exec
INSERT INTO operation_refunds(space_id,operation_id,revision,purchase_id,purchase_revision,purchase_minor)
SELECT f.space_id,f.operation_id,f.revision+1,f.purchase_id,f.purchase_revision,f.purchase_minor
FROM operation_refunds f WHERE f.space_id=$1 AND f.operation_id=$2 AND f.revision=$3;

-- name: HasRefundLink :one
SELECT EXISTS(SELECT 1 FROM operation_refunds WHERE space_id=$1 AND operation_id=$2 AND revision=$3);

-- name: BalanceAtDate :one
SELECT COALESCE((a.opening_minor::numeric+COALESCE((SELECT sum(e.amount_minor) FROM position_effects e
JOIN financial_operations o ON (o.space_id,o.id,o.current_revision)=(e.space_id,e.operation_id,e.revision)
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(e.space_id,e.operation_id,e.revision)
WHERE e.space_id=a.space_id AND e.account_id=a.id AND e.in_tracking AND NOT r.voided AND r.effective_date<=sqlc.arg(effective_date)),0))::text,'')::text AS amount_minor
FROM financial_accounts a WHERE a.space_id=sqlc.arg(space_id) AND a.id=sqlc.arg(account_id);

-- name: InsertReconciliation :exec
INSERT INTO account_reconciliations(id,space_id,account_id,effective_date,calculated_minor,observed_minor,difference_minor,adjustment_id,author,reason)
VALUES($1,$2,$3,$4,$5,$6,NULLIF(sqlc.arg(difference)::text,'')::numeric,$7,$8,$9);

-- name: AccountReconciliations :many
SELECT r.id,r.effective_date,r.calculated_minor,r.observed_minor,COALESCE(r.difference_minor::text,'')::text AS difference,r.adjustment_id,r.author,r.reason,r.recorded_at,
EXISTS(SELECT 1 FROM reconciliation_reviews v WHERE (v.space_id,v.reconciliation_id)=(r.space_id,r.id))::boolean AS needs_review
FROM account_reconciliations r WHERE r.space_id=$1 AND r.account_id=$2 AND r.id>$3 ORDER BY r.id LIMIT $4;

-- name: MarkAffectedReconciliations :exec
INSERT INTO reconciliation_reviews(space_id,reconciliation_id,operation_id,revision)
SELECT r.space_id,r.id,sqlc.arg(operation_id)::text,sqlc.arg(new_revision)::bigint
FROM account_reconciliations r WHERE r.space_id=sqlc.arg(space_id)
AND r.account_id IN (SELECT changed.account_id FROM position_effects changed
 WHERE changed.space_id=r.space_id AND changed.operation_id=sqlc.arg(operation_id)
 AND changed.revision IN (sqlc.arg(old_revision)::bigint,sqlc.arg(new_revision)::bigint))
AND COALESCE((SELECT sum(e.amount_minor) FROM position_effects e JOIN operation_revisions v
 ON (v.space_id,v.operation_id,v.revision)=(e.space_id,e.operation_id,e.revision)
 WHERE e.space_id=r.space_id AND e.account_id=r.account_id AND e.operation_id=sqlc.arg(operation_id)
 AND e.revision=sqlc.arg(old_revision)::bigint AND e.in_tracking AND NOT v.voided AND v.effective_date<=r.effective_date),0)
<>COALESCE((SELECT sum(e.amount_minor) FROM position_effects e JOIN operation_revisions v
 ON (v.space_id,v.operation_id,v.revision)=(e.space_id,e.operation_id,e.revision)
 WHERE e.space_id=r.space_id AND e.account_id=r.account_id AND e.operation_id=sqlc.arg(operation_id)
 AND e.revision=sqlc.arg(new_revision)::bigint AND e.in_tracking AND NOT v.voided AND v.effective_date<=r.effective_date),0)
ON CONFLICT DO NOTHING;

-- name: ReconciledBalanceAffected :one
SELECT EXISTS(SELECT 1 FROM account_reconciliations r WHERE r.space_id=sqlc.arg(space_id) AND r.account_id=sqlc.arg(account_id)
AND CASE WHEN sqlc.arg(before_date)::date<=r.effective_date THEN sqlc.arg(before_minor)::text::numeric ELSE 0 END
 <> CASE WHEN sqlc.arg(after_date)::date<=r.effective_date THEN sqlc.arg(after_minor)::text::numeric ELSE 0 END);

-- name: InsertConsistencyCheck :exec
INSERT INTO balance_consistency_checks(id,space_id,account_id,projected_minor,reconstructed_minor,discrepancy,author)
VALUES($1,$2,$3,$4,NULLIF(sqlc.arg(reconstructed)::text,'')::numeric,$5,$6);

-- name: AccountConsistencyChecks :many
SELECT c.id,c.projected_minor,COALESCE(c.reconstructed_minor::text,'')::text AS reconstructed,c.discrepancy,c.author,c.recorded_at
FROM balance_consistency_checks c WHERE c.space_id=$1 AND c.account_id=$2 AND c.id>$3 ORDER BY c.id LIMIT $4;

-- name: SearchOperations :many
SELECT o.id,o.kind,r.revision,r.effective_date,r.note,r.exceptional,r.merchant_id,r.counterparty_id,
e.amount_minor,a.currency
FROM financial_operations o
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(o.space_id,o.id,o.current_revision)
JOIN position_effects e ON (e.space_id,e.operation_id,e.revision,e.ordinal)=(r.space_id,r.operation_id,r.revision,0)
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
WHERE o.space_id=sqlc.arg(space_id) AND NOT r.voided
AND r.effective_date>=sqlc.arg(from_date)::date AND r.effective_date<sqlc.arg(to_date)::date
AND (r.effective_date,o.id)>(sqlc.arg(cursor_date)::date,sqlc.arg(cursor_id)::text)
AND (sqlc.arg(kind)::text='' OR o.kind=sqlc.arg(kind))
AND (sqlc.arg(merchant_id)::text='' OR (SELECT COALESCE(k.merged_into,k.id) FROM financial_catalog k WHERE k.space_id=r.space_id AND k.id=r.merchant_id)=(SELECT COALESCE(k.merged_into,k.id) FROM financial_catalog k WHERE k.space_id=r.space_id AND k.id=sqlc.arg(merchant_id)))
AND (sqlc.arg(counterparty_id)::text='' OR (SELECT COALESCE(k.merged_into,k.id) FROM financial_catalog k WHERE k.space_id=r.space_id AND k.id=r.counterparty_id)=(SELECT COALESCE(k.merged_into,k.id) FROM financial_catalog k WHERE k.space_id=r.space_id AND k.id=sqlc.arg(counterparty_id)))
AND (sqlc.arg(tag_id)::text='' OR EXISTS(SELECT 1 FROM operation_tags t JOIN financial_catalog k ON (k.space_id,k.id)=(t.space_id,t.tag_id)
 WHERE (t.space_id,t.operation_id,t.revision)=(r.space_id,r.operation_id,r.revision) AND COALESCE(k.merged_into,k.id)=COALESCE((SELECT COALESCE(z.merged_into,z.id) FROM financial_catalog z WHERE z.space_id=r.space_id AND z.id=sqlc.arg(tag_id)),'')))
AND (sqlc.arg(category_id)::text='' OR EXISTS(SELECT 1 FROM economic_components c JOIN financial_catalog k ON (k.space_id,k.id)=(c.space_id,c.category_id)
 WHERE (c.space_id,c.operation_id,c.revision)=(r.space_id,r.operation_id,r.revision) AND COALESCE(k.merged_into,k.id)=COALESCE((SELECT COALESCE(z.merged_into,z.id) FROM financial_catalog z WHERE z.space_id=r.space_id AND z.id=sqlc.arg(category_id)),'')))
AND EXISTS(SELECT 1 FROM position_effects f JOIN financial_accounts b ON (b.space_id,b.id)=(f.space_id,f.account_id)
 WHERE (f.space_id,f.operation_id,f.revision)=(r.space_id,r.operation_id,r.revision)
 AND (sqlc.arg(account_id)::text='' OR f.account_id=sqlc.arg(account_id))
 AND (sqlc.arg(currency)::text='' OR b.currency=sqlc.arg(currency))
 AND abs(f.amount_minor::numeric)>=sqlc.arg(min_minor)::text::numeric AND abs(f.amount_minor::numeric)<=sqlc.arg(max_minor)::text::numeric)
AND (sqlc.arg(query)::text='' OR strpos(lower(r.note),lower(sqlc.arg(query)))>0 OR EXISTS(SELECT 1 FROM financial_catalog k
 LEFT JOIN financial_catalog canonical ON (canonical.space_id,canonical.id)=(k.space_id,k.merged_into)
 WHERE k.space_id=r.space_id AND k.id IN (r.merchant_id,r.counterparty_id)
 AND (strpos(lower(k.name),lower(sqlc.arg(query)))>0 OR strpos(lower(canonical.name),lower(sqlc.arg(query)))>0)))
AND NOT EXISTS(SELECT 1 FROM position_effects f JOIN financial_accounts b ON (b.space_id,b.id)=(f.space_id,f.account_id)
 WHERE (f.space_id,f.operation_id,f.revision)=(r.space_id,r.operation_id,r.revision)
 AND NOT (b.responsible_member=sqlc.arg(member_id) OR b.visibility='space' OR
 (b.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(b.space_id,b.id,sqlc.arg(member_id))))))
ORDER BY r.effective_date,o.id LIMIT sqlc.arg(page_size);

-- name: RecentOperationSelections :many
SELECT o.id,r.effective_date,r.recorded_at,e.account_id,
CASE WHEN EXISTS(SELECT 1 FROM economic_components more WHERE (more.space_id,more.operation_id,more.revision)=(r.space_id,r.operation_id,r.revision) AND more.ordinal>0) THEN '' ELSE COALESCE(k.merged_into,k.id,'') END::text AS category_id,
COALESCE(m.merged_into,m.id,'')::text AS merchant_id
FROM financial_operations o JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(o.space_id,o.id,o.current_revision)
JOIN position_effects e ON (e.space_id,e.operation_id,e.revision,e.ordinal)=(r.space_id,r.operation_id,r.revision,0)
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
JOIN economic_components c ON (c.space_id,c.operation_id,c.revision,c.ordinal)=(r.space_id,r.operation_id,r.revision,0)
LEFT JOIN financial_catalog k ON (k.space_id,k.id)=(c.space_id,c.category_id)
LEFT JOIN financial_catalog target ON (target.space_id,target.id)=(k.space_id,k.merged_into)
LEFT JOIN financial_catalog m ON (m.space_id,m.id)=(r.space_id,r.merchant_id)
WHERE o.space_id=sqlc.arg(space_id) AND o.kind=sqlc.arg(kind) AND r.author=sqlc.arg(author) AND NOT r.voided
AND a.state='active' AND EXISTS(SELECT 1 FROM memberships rm WHERE rm.space_id=a.space_id AND rm.id=a.responsible_member AND rm.ended_at IS NULL AND rm.role<>'reader')
AND (sqlc.arg(merchant_id)::text='' OR COALESCE(m.merged_into,m.id)=COALESCE((SELECT COALESCE(z.merged_into,z.id) FROM financial_catalog z WHERE z.space_id=r.space_id AND z.id=sqlc.arg(merchant_id)),''))
AND (k.id IS NULL OR NOT COALESCE(target.archived,k.archived))
AND (m.id IS NULL OR NOT COALESCE((SELECT z.archived FROM financial_catalog z WHERE (z.space_id,z.id)=(m.space_id,m.merged_into)),m.archived))
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' AND a.space_permission='operate' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)) AND g.permission='operate')))
ORDER BY r.recorded_at DESC,o.id DESC LIMIT sqlc.arg(page_size);

-- name: PositionTotals :many
SELECT CASE WHEN a.kind IN ('cash','bank','savings','wallet') THEN 'liquid'
WHEN a.kind IN ('credit_card','simple_debt','loan','financed_purchase') THEN 'obligation' ELSE 'receivable' END::text AS position,
a.currency,a.scale,COALESCE(sum(a.balance_minor),0)::text AS known_minor,
bool_and(a.quality='confirmed')::boolean AS confirmed,bool_and(a.balance_minor IS NOT NULL)::boolean AS complete
FROM financial_accounts a WHERE a.space_id=sqlc.arg(space_id)
AND (sqlc.arg(account_id)::text='' OR a.id=sqlc.arg(account_id)) AND (sqlc.arg(currency)::text='' OR a.currency=sqlc.arg(currency))
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)))))
GROUP BY 1,a.currency,a.scale ORDER BY 1,a.currency;

-- name: EconomicTotals :many
SELECT a.currency,a.scale,c.direction,sum(c.amount_minor)::text AS amount_minor
FROM economic_components c
JOIN financial_operations o ON (o.space_id,o.id,o.current_revision)=(c.space_id,c.operation_id,c.revision)
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(c.space_id,c.operation_id,c.revision)
JOIN position_effects e ON (e.space_id,e.operation_id,e.revision,e.ordinal)=(c.space_id,c.operation_id,c.revision,c.effect_ordinal)
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
LEFT JOIN financial_catalog k ON (k.space_id,k.id)=(c.space_id,c.category_id)
WHERE c.space_id=sqlc.arg(space_id) AND NOT r.voided AND e.in_tracking
AND r.effective_date>=sqlc.arg(from_date)::date AND r.effective_date<sqlc.arg(to_date)::date
AND (sqlc.arg(account_id)::text='' OR a.id=sqlc.arg(account_id)) AND (sqlc.arg(currency)::text='' OR a.currency=sqlc.arg(currency))
AND (sqlc.arg(category_id)::text='' OR COALESCE(k.merged_into,k.id)=(SELECT COALESCE(z.merged_into,z.id) FROM financial_catalog z WHERE z.space_id=c.space_id AND z.id=sqlc.arg(category_id)))
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)))))
GROUP BY a.currency,a.scale,c.direction ORDER BY a.currency,c.direction;

-- name: CashTotals :many
SELECT a.currency,a.scale,e.kind,sum(e.amount_minor)::text AS amount_minor
FROM position_effects e JOIN financial_operations o ON (o.space_id,o.id,o.current_revision)=(e.space_id,e.operation_id,e.revision)
JOIN operation_revisions r ON (r.space_id,r.operation_id,r.revision)=(e.space_id,e.operation_id,e.revision)
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
WHERE e.space_id=sqlc.arg(space_id) AND NOT r.voided AND e.in_tracking AND a.kind IN ('cash','bank','savings','wallet')
AND r.effective_date>=sqlc.arg(from_date)::date AND r.effective_date<sqlc.arg(to_date)::date
AND (sqlc.arg(account_id)::text='' OR a.id=sqlc.arg(account_id)) AND (sqlc.arg(currency)::text='' OR a.currency=sqlc.arg(currency))
AND (a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)))))
GROUP BY a.currency,a.scale,e.kind ORDER BY a.currency,e.kind;
