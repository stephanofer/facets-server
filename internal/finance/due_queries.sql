-- name: InsertDueItem :exec
INSERT INTO due_items(id,space_id,account_id,direction,name,due_date,certainty,amount_minor,maximum_minor,author,currency,scale,resource_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13);

-- name: AuthorizedDueItems :many
SELECT sqlc.embed(d),d.currency,d.scale,
 COALESCE(r.series_id,'')::text AS series_id,COALESCE(r.series_version,0)::bigint AS series_version,
 COALESCE(r.ordinal,0)::integer AS occurrence_ordinal,COALESCE(r.slot,0)::integer AS occurrence_slot,
 paid.amount::text AS applied_minor,
 COALESCE(v.counterparty_id,'')::text AS counterparty_id,COALESCE(v.suggested_category_id,'')::text AS suggested_category_id,
 COALESCE(v.subscription_plan,'')::text AS subscription_plan,
 COALESCE(charge.operation_id,'')::text AS charge_operation_id,charge.effective_date AS charge_date,
 COALESCE(charge.amount_minor,'')::text AS charge_minor,COALESCE(charge.currency,'')::text AS charge_currency,
 COALESCE(rem.id,'')::text AS reminder_id,COALESCE(rem.version,0)::bigint AS reminder_version,
 COALESCE(rem.enabled,false)::boolean AS reminder_enabled,rem.scheduled_at AS reminder_at,
 rem.due_date AS reminder_due_date,delivery.delivered_at AS reminder_delivered_at
FROM due_items d LEFT JOIN financial_accounts a ON (a.space_id,a.id)=(d.space_id,d.account_id)
LEFT JOIN planning_resources pr ON (pr.space_id,pr.id)=(d.space_id,d.resource_id)
LEFT JOIN recurring_occurrences r ON (r.space_id,r.due_id)=(d.space_id,d.id)
LEFT JOIN recurring_series_versions v ON (v.space_id,v.series_id,v.version)=(r.space_id,r.series_id,r.series_version)
LEFT JOIN due_reminders rem ON (rem.space_id,rem.due_id,rem.member_id)=(d.space_id,d.id,sqlc.arg(member_id))
LEFT JOIN reminder_deliveries delivery ON (delivery.space_id,delivery.reminder_id,delivery.version)=(rem.space_id,rem.id,rem.version)
LEFT JOIN LATERAL (SELECT p.operation_id,rev.effective_date,abs(e.amount_minor::numeric)::text AS amount_minor,account.currency
 FROM payment_applications p JOIN financial_operations op ON (op.space_id,op.id)=(p.space_id,p.operation_id)
 JOIN operation_revisions rev ON (rev.space_id,rev.operation_id,rev.revision)=(op.space_id,op.id,op.current_revision)
 JOIN position_effects e ON (e.space_id,e.operation_id,e.revision,e.ordinal)=(op.space_id,op.id,op.current_revision,p.effect_ordinal)
 JOIN financial_accounts account ON (account.space_id,account.id)=(e.space_id,e.account_id)
 WHERE (p.space_id,p.due_id)=(d.space_id,d.id) AND NOT EXISTS
 (SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))
 ORDER BY rev.effective_date DESC,p.id DESC LIMIT 1) charge ON true
CROSS JOIN LATERAL (SELECT COALESCE(sum(p.due_minor),0) AS amount FROM payment_applications p
 WHERE (p.space_id,p.due_id)=(d.space_id,d.id) AND NOT EXISTS
 (SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))) paid
WHERE d.space_id=sqlc.arg(space_id)
AND (NOT sqlc.arg(charge_only)::boolean OR charge.operation_id IS NOT NULL)
AND (sqlc.arg(id)::text='' OR d.id=sqlc.arg(id))
AND (sqlc.arg(account_filter)::text='' OR d.account_id=sqlc.arg(account_filter))
AND (sqlc.arg(series_filter)::text='' OR r.series_id=sqlc.arg(series_filter))
AND (sqlc.arg(direction_filter)::text='' OR d.direction=sqlc.arg(direction_filter))
AND (sqlc.arg(currency_filter)::text='' OR d.currency=sqlc.arg(currency_filter))
AND (NOT sqlc.arg(unassigned_filter)::boolean OR d.account_id IS NULL)
AND (sqlc.arg(reminder_filter)::text='' OR
 (sqlc.arg(reminder_filter)='delivered' AND delivery.delivered_at IS NOT NULL) OR
 (sqlc.arg(reminder_filter)='stale' AND rem.due_date<>d.due_date) OR
 (sqlc.arg(reminder_filter)='scheduled' AND rem.enabled AND rem.due_date=d.due_date AND delivery.delivered_at IS NULL
  AND d.state='open' AND NOT(d.certainty='confirmed' AND paid.amount>=d.amount_minor)))
AND (sqlc.arg(origin_filter)::text='' OR (sqlc.arg(origin_filter)='recurring' AND r.series_id IS NOT NULL)
 OR (sqlc.arg(origin_filter)='standalone' AND r.series_id IS NULL))
AND (sqlc.arg(status_filter)::text='' OR sqlc.arg(status_filter)=(CASE
 WHEN d.state<>'open' THEN d.state
 WHEN d.certainty='confirmed' AND paid.amount>=d.amount_minor THEN 'paid'
 WHEN paid.amount>0 THEN 'partial'
 WHEN d.due_date<sqlc.arg(today)::date THEN 'overdue'
 WHEN d.due_date<=sqlc.arg(today)::date+7 THEN 'upcoming'
 ELSE 'pending' END))
AND (sqlc.arg(application_operation)::text='' OR EXISTS(SELECT 1 FROM payment_applications selected
 WHERE (selected.space_id,selected.due_id)=(d.space_id,d.id) AND selected.operation_id=sqlc.arg(application_operation)
 AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(selected.space_id,selected.id))))
AND (sqlc.arg(after_date)::date IS NULL OR (d.due_date,d.id)>(sqlc.arg(after_date),sqlc.arg(after_id)::text))
AND (sqlc.arg(from_date)::date IS NULL OR d.due_date>=sqlc.arg(from_date))
AND (sqlc.arg(until_date)::date IS NULL OR d.due_date<sqlc.arg(until_date))
AND ((a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g
   WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)))))
 OR (pr.responsible_member=sqlc.arg(member_id) OR pr.visibility='space' OR
  (pr.visibility='selected' AND EXISTS(SELECT 1 FROM planning_readers g
   WHERE (g.space_id,g.resource_id,g.member_id)=(pr.space_id,pr.id,sqlc.arg(member_id))))))
AND NOT EXISTS(
 SELECT 1 FROM payment_applications p JOIN financial_operations o ON (o.space_id,o.id)=(p.space_id,p.operation_id)
 JOIN position_effects e ON (e.space_id,e.operation_id,e.revision)=(o.space_id,o.id,o.current_revision)
 JOIN financial_accounts related ON (related.space_id,related.id)=(e.space_id,e.account_id)
 WHERE (p.space_id,p.due_id)=(d.space_id,d.id)
 AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))
 AND NOT (related.responsible_member=sqlc.arg(member_id) OR related.visibility='space' OR
  (related.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g
   WHERE (g.space_id,g.account_id,g.member_id)=(related.space_id,related.id,sqlc.arg(member_id))))))
ORDER BY CASE WHEN sqlc.arg(charge_only)::boolean THEN charge.effective_date END DESC,
 CASE WHEN sqlc.arg(charge_only)::boolean THEN charge.operation_id END DESC,
 d.due_date,d.id LIMIT sqlc.arg(page_size);

-- name: LockDueItem :one
SELECT * FROM due_items WHERE space_id=$1 AND id=$2 FOR UPDATE;

-- name: EditDueItem :execrows
UPDATE due_items SET name=$4,due_date=$5,certainty=$6,amount_minor=$7,maximum_minor=$8,state=$9,version=version+1
WHERE space_id=$1 AND id=$2 AND version=$3;

-- name: AdvanceDueItem :exec
UPDATE due_items SET version=version+1 WHERE space_id=$1 AND id=$2;

-- name: CaptureDueItem :exec
INSERT INTO due_item_revisions(space_id,due_id,version,name,due_date,certainty,amount_minor,maximum_minor,state,author,reason)
SELECT d.space_id,d.id,d.version,d.name,d.due_date,d.certainty,d.amount_minor,d.maximum_minor,d.state,sqlc.arg(author),sqlc.arg(reason)
FROM due_items d WHERE d.space_id=sqlc.arg(space_id) AND d.id=sqlc.arg(id);

-- name: LockApplicationOperation :one
SELECT * FROM financial_operations WHERE space_id=$1 AND id=$2 FOR UPDATE;

-- name: InsertPaymentApplication :exec
INSERT INTO payment_applications(id,space_id,due_id,operation_id,source_revision,effect_ordinal,movement_minor,due_minor,author)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);

-- name: MovementAppliedMinor :one
SELECT COALESCE(sum(p.movement_minor),0)::text FROM payment_applications p
WHERE p.space_id=$1 AND p.operation_id=$2 AND p.effect_ordinal=$3
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id));

-- name: DueApplications :many
SELECT p.* FROM payment_applications p WHERE p.space_id=$1 AND p.due_id=$2
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))
ORDER BY p.id LIMIT $3;

-- name: DueApplicationCount :one
SELECT count(*) FROM payment_applications p WHERE p.space_id=$1 AND p.due_id=$2
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id));

-- name: OperationApplicationCount :one
SELECT count(*) FROM payment_applications p WHERE p.space_id=$1 AND p.operation_id=$2
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id));

-- name: PaymentApplication :one
SELECT p.* FROM payment_applications p WHERE p.space_id=$1 AND p.due_id=$2 AND p.id=$3;

-- name: ReleasePaymentApplication :execrows
INSERT INTO payment_application_releases(space_id,application_id,author,reason) VALUES ($1,$2,$3,$4)
ON CONFLICT(space_id,application_id) DO NOTHING;

-- name: OperationApplicationBuckets :many
SELECT e.account_id,a.currency,p.effect_ordinal,COALESCE(sum(p.movement_minor),0)::text AS applied_minor
FROM payment_applications p JOIN position_effects e
 ON (e.space_id,e.operation_id,e.revision,e.ordinal)=(p.space_id,p.operation_id,p.source_revision,p.effect_ordinal)
JOIN financial_accounts a ON (a.space_id,a.id)=(e.space_id,e.account_id)
WHERE p.space_id=$1 AND p.operation_id=$2
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))
GROUP BY e.account_id,a.currency,p.effect_ordinal ORDER BY p.effect_ordinal;

-- name: OperationAppliedDues :many
SELECT DISTINCT d.id FROM due_items d JOIN payment_applications p ON (p.space_id,p.due_id)=(d.space_id,d.id)
WHERE p.space_id=$1 AND p.operation_id=$2
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))
ORDER BY d.id;

-- name: ReleaseVoidedApplications :exec
INSERT INTO payment_application_releases(space_id,application_id,author,reason)
SELECT p.space_id,p.id,sqlc.arg(author),'Source operation voided' FROM payment_applications p
WHERE p.space_id=sqlc.arg(space_id) AND p.operation_id=sqlc.arg(operation_id)
AND NOT EXISTS(SELECT 1 FROM payment_application_releases x WHERE (x.space_id,x.application_id)=(p.space_id,p.id))
ON CONFLICT(space_id,application_id) DO NOTHING;
