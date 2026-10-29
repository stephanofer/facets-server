-- name: InsertProbe :exec
INSERT INTO environment_probe (id, label) VALUES ($1, $2);

-- name: GetProbe :one
SELECT id, label FROM environment_probe WHERE id = $1;
