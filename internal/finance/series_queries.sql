-- name: InsertRecurringSeries :exec
INSERT INTO recurring_series(id,space_id,account_id,direction,author,currency,scale,resource_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: InsertSeriesVersion :exec
INSERT INTO recurring_series_versions(space_id,series_id,version,name,certainty,amount_minor,maximum_minor,start_date,frequency,interval_count,first_day,second_day,end_date,repetition_count,effective_from,generated_until,state,author,reason,counterparty_id,suggested_category_id,subscription_plan,trial_until,renewal_on,cancel_by,cancellation_on)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25);

-- name: AuthorizedSeries :many
SELECT sqlc.embed(s),sqlc.embed(v),s.currency,s.scale
FROM recurring_series s
JOIN recurring_series_versions v ON (v.space_id,v.series_id,v.version)=(s.space_id,s.id,s.current_version)
LEFT JOIN financial_accounts a ON (a.space_id,a.id)=(s.space_id,s.account_id)
LEFT JOIN planning_resources pr ON (pr.space_id,pr.id)=(s.space_id,s.resource_id)
WHERE s.space_id=sqlc.arg(space_id)
AND (sqlc.arg(id)::text='' OR s.id=sqlc.arg(id))
AND (sqlc.arg(after_id)::text='' OR s.id>sqlc.arg(after_id))
AND ((a.responsible_member=sqlc.arg(member_id) OR a.visibility='space' OR
 (a.visibility='selected' AND EXISTS(SELECT 1 FROM account_grants g
  WHERE (g.space_id,g.account_id,g.member_id)=(a.space_id,a.id,sqlc.arg(member_id)))))
 OR (pr.responsible_member=sqlc.arg(member_id) OR pr.visibility='space' OR
  (pr.visibility='selected' AND EXISTS(SELECT 1 FROM planning_readers g
   WHERE (g.space_id,g.resource_id,g.member_id)=(pr.space_id,pr.id,sqlc.arg(member_id))))))
ORDER BY s.id LIMIT sqlc.arg(page_size);

-- name: LockRecurringSeries :one
SELECT * FROM recurring_series WHERE space_id=$1 AND id=$2 FOR UPDATE;

-- name: AdvanceSeriesVersion :execrows
UPDATE recurring_series SET current_version=current_version+1 WHERE space_id=$1 AND id=$2 AND current_version=$3;

-- name: AdvanceSeriesHorizon :exec
UPDATE recurring_series_versions SET generated_until=$4 WHERE space_id=$1 AND series_id=$2 AND version=$3;

-- name: InsertSeriesDues :exec
INSERT INTO due_items(id,space_id,account_id,direction,name,due_date,certainty,amount_minor,maximum_minor,author,currency,scale,resource_id)
SELECT unnest(sqlc.arg(ids)::text[]),sqlc.arg(space_id)::text,sqlc.narg(account_id)::text,
 sqlc.arg(direction)::text,sqlc.arg(name)::text,unnest(sqlc.arg(dates)::date[]),
 sqlc.arg(certainty)::text,sqlc.narg(amount_minor)::bigint,sqlc.narg(maximum_minor)::bigint,sqlc.arg(author)::text,
 sqlc.arg(currency)::text,sqlc.arg(scale)::integer,sqlc.narg(resource_id)::text;

-- name: InsertRecurringOccurrences :exec
INSERT INTO recurring_occurrences(space_id,series_id,series_version,ordinal,slot,due_id)
SELECT sqlc.arg(space_id)::text,sqlc.arg(series_id)::text,sqlc.arg(series_version)::bigint,
 unnest(sqlc.arg(ordinals)::integer[]),unnest(sqlc.arg(slots)::integer[]),unnest(sqlc.arg(ids)::text[]);

-- name: CaptureSeriesDues :exec
INSERT INTO due_item_revisions(space_id,due_id,version,name,due_date,certainty,amount_minor,maximum_minor,state,author,reason)
SELECT d.space_id,d.id,d.version,d.name,d.due_date,d.certainty,d.amount_minor,d.maximum_minor,d.state,
 sqlc.arg(author)::text,'Generated recurring expectation'
FROM due_items d WHERE d.space_id=sqlc.arg(space_id) AND d.id=ANY(sqlc.arg(ids)::text[]);

-- name: SeriesVersions :many
SELECT * FROM recurring_series_versions WHERE space_id=$1 AND series_id=$2 AND version>$3 ORDER BY version LIMIT $4;
