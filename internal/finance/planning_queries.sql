-- name: InsertPlanningResource :exec
INSERT INTO planning_resources(id,space_id,responsible_member) VALUES ($1,$2,$3);

-- name: PlanningResource :one
SELECT sqlc.embed(p),(m.ended_at IS NULL AND m.role<>'reader')::boolean AS responsible_active
FROM planning_resources p JOIN memberships m ON (m.space_id,m.id)=(p.space_id,p.responsible_member)
WHERE p.space_id=$1 AND p.id=$2
AND (p.responsible_member=sqlc.arg(member_id) OR p.visibility='space' OR
 (p.visibility='selected' AND EXISTS(SELECT 1 FROM planning_readers r WHERE (r.space_id,r.resource_id,r.member_id)=(p.space_id,p.id,sqlc.arg(member_id)))))
FOR UPDATE OF p;

-- name: PlanningReaders :many
SELECT member_id FROM planning_readers WHERE space_id=$1 AND resource_id=$2 ORDER BY member_id;

-- name: ClearPlanningReaders :exec
DELETE FROM planning_readers WHERE space_id=$1 AND resource_id=$2;

-- name: PutPlanningReaders :exec
INSERT INTO planning_readers(space_id,resource_id,member_id) SELECT sqlc.arg(space_id)::text,sqlc.arg(resource_id)::text,unnest(sqlc.arg(member_ids)::text[]);

-- name: UpdatePlanningAccess :execrows
UPDATE planning_resources SET responsible_member=$4,visibility=$5,version=version+1 WHERE space_id=$1 AND id=$2 AND version=$3;

-- name: CapturePlanningAccess :exec
INSERT INTO planning_access_history(space_id,resource_id,version,responsible_member,visibility,readers,author,reason)
SELECT p.space_id,p.id,p.version,p.responsible_member,p.visibility,
 ARRAY(SELECT r.member_id FROM planning_readers r WHERE (r.space_id,r.resource_id)=(p.space_id,p.id) ORDER BY r.member_id),sqlc.arg(author),sqlc.arg(reason)
FROM planning_resources p WHERE p.space_id=sqlc.arg(space_id) AND p.id=sqlc.arg(id);

-- name: RecoverablePlanningResources :many
SELECT p.id,p.version FROM planning_resources p JOIN memberships m ON (m.space_id,m.id)=(p.space_id,p.responsible_member)
WHERE p.space_id=$1 AND m.principal_id=$2 AND m.ended_at IS NOT NULL AND p.id>$3 ORDER BY p.id LIMIT $4;

-- name: RecoverablePlanningResource :one
SELECT p.* FROM planning_resources p JOIN memberships m ON (m.space_id,m.id)=(p.space_id,p.responsible_member)
WHERE p.space_id=$1 AND p.id=$2 AND m.principal_id=$3 AND m.ended_at IS NOT NULL FOR UPDATE OF p;
