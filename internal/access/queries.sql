-- name: InsertPrincipal :execrows
INSERT INTO principals(id,realm,email,password_hash,display_name,state,locale,zone,currency)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(realm,email) DO NOTHING;
-- name: FindPrincipal :one
SELECT * FROM principals WHERE realm=$1 AND email=$2;
-- name: GetPrincipal :one
SELECT * FROM principals WHERE id=$1 FOR UPDATE;
-- name: ReadPrincipal :one
SELECT * FROM principals WHERE id=$1 FOR SHARE;
-- name: ActivatePrincipal :exec
UPDATE principals SET state='active',version=version+1 WHERE id=$1;
-- name: ChangePassword :exec
UPDATE principals SET password_hash=$2,version=version+1 WHERE id=$1;
-- name: ChangeEmail :exec
UPDATE principals SET email=$2,version=version+1 WHERE id=$1;
-- name: Preferences :execrows
UPDATE principals SET display_name=$3,locale=$4,zone=$5,notifications=$6,version=version+1
WHERE id=$1 AND version=$2;
-- name: PendingMFA :exec
UPDATE principals SET mfa_pending=$2,mfa_pending_until=$3 WHERE id=$1;
-- name: SetMFA :exec
UPDATE principals SET mfa_secret=$2,mfa_step=$3,mfa_pending=NULL,mfa_pending_until=NULL,version=version+1 WHERE id=$1;
-- name: SetMFAStep :exec
UPDATE principals SET mfa_step=$2 WHERE id=$1;
-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes(principal_id,verifier) VALUES ($1,$2);
-- name: InvalidateRecoveryCodes :exec
UPDATE recovery_codes SET used_at=clock_timestamp() WHERE principal_id=$1 AND used_at IS NULL;
-- name: UseRecoveryCode :execrows
UPDATE recovery_codes SET used_at=clock_timestamp() WHERE principal_id=$1 AND verifier=$2 AND used_at IS NULL;
-- name: FindSession :one
SELECT * FROM sessions WHERE verifier=$1;
-- name: LockSession :one
SELECT * FROM sessions WHERE id=$1 FOR UPDATE;
-- name: InsertSession :exec
INSERT INTO sessions(id,principal_id,verifier,device,created_at,seen_at,expires_at,reauthenticated_at)
VALUES ($1,$2,$3,$4,$5,$5,$6,$5);
-- name: TouchSession :exec
UPDATE sessions SET seen_at=$2 WHERE id=$1;
-- name: RevokeSessions :exec
UPDATE sessions SET revoked_at=clock_timestamp() WHERE principal_id=$1 AND revoked_at IS NULL;
-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at=clock_timestamp() WHERE principal_id=$1 AND id=$2 AND revoked_at IS NULL;
-- name: ListSessions :many
SELECT id,device,created_at,seen_at,expires_at FROM sessions
WHERE principal_id=$1 AND revoked_at IS NULL AND expires_at>$2 AND seen_at>$3
ORDER BY id LIMIT $4;
-- name: CountSessions :one
SELECT count(*) FROM sessions WHERE principal_id=$1 AND revoked_at IS NULL AND expires_at>$2 AND seen_at>$3;
-- name: SetContext :exec
UPDATE sessions SET active_space=$2 WHERE id=$1;
-- name: InsertToken :exec
INSERT INTO identity_tokens(id,principal_id,purpose,verifier,target_email,expires_at)
VALUES ($1,$2,$3,$4,$5,$6);
-- name: FindToken :one
SELECT * FROM identity_tokens WHERE verifier=$1 AND purpose=$2;
-- name: LockToken :one
SELECT * FROM identity_tokens WHERE id=$1 FOR UPDATE;
-- name: InvalidateTokens :exec
UPDATE identity_tokens SET invalidated_at=clock_timestamp() WHERE principal_id=$1 AND purpose=$2 AND consumed_at IS NULL AND invalidated_at IS NULL;
-- name: ConsumeToken :exec
UPDATE identity_tokens SET consumed_at=clock_timestamp() WHERE id=$1;
-- name: LatestToken :one
SELECT created_at FROM identity_tokens WHERE principal_id=$1 AND purpose=$2 ORDER BY created_at DESC LIMIT 1;
-- name: TakeLimit :one
INSERT INTO access_limits(bucket,attempts,expires_at) VALUES ($1,1,$2)
ON CONFLICT(bucket) DO UPDATE SET
attempts=CASE WHEN access_limits.expires_at<=$3 THEN 1 ELSE LEAST(access_limits.attempts+1,10001) END,
expires_at=CASE WHEN access_limits.expires_at<=$3 THEN EXCLUDED.expires_at ELSE access_limits.expires_at END
RETURNING attempts;
-- name: InsertDelivery :exec
INSERT INTO access_deliveries(id,kind,resource_ref,principal_id,target_email,ciphertext,expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7);
-- name: GetDelivery :one
SELECT * FROM access_deliveries WHERE id=$1;
-- name: ClearDelivery :exec
UPDATE access_deliveries SET ciphertext=''::bytea WHERE id=$1;
-- name: PurgeDeliverySecrets :execrows
UPDATE access_deliveries SET ciphertext=''::bytea WHERE id IN
(SELECT id FROM access_deliveries WHERE expires_at<=clock_timestamp() AND octet_length(ciphertext)>0 ORDER BY expires_at LIMIT 1000);
-- name: PurgeLimits :execrows
DELETE FROM access_limits WHERE expires_at<clock_timestamp() AND bucket IN
(SELECT bucket FROM access_limits WHERE expires_at<clock_timestamp() ORDER BY expires_at LIMIT 1000);
-- name: InsertSpace :exec
INSERT INTO spaces(id,name,purpose,currency,zone,locale,period_day,new_account_visibility,personal_for)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9);
-- name: GetSpace :one
SELECT * FROM spaces WHERE id=$1 FOR UPDATE;
-- name: ListSpaces :many
SELECT s.*,m.id AS membership_id,m.role FROM spaces s JOIN memberships m ON m.space_id=s.id
JOIN space_access a ON a.space_id=s.id
JOIN space_capabilities c ON c.space_id=s.id AND c.capability='read'
WHERE m.principal_id=$1 AND m.ended_at IS NULL AND s.id>$2
AND a.state='active' AND a.valid_from<=clock_timestamp() AND a.valid_until>clock_timestamp()
AND c.ceiling>0 AND c.used<=c.ceiling
ORDER BY s.id LIMIT $3;
-- name: EditSpace :execrows
UPDATE spaces SET name=$3,purpose=$4,currency=$5,zone=$6,locale=$7,period_day=$8,new_account_visibility=$9,version=version+1
WHERE id=$1 AND version=$2;
-- name: InsertMembership :exec
INSERT INTO memberships(id,space_id,principal_id,role) VALUES ($1,$2,$3,$4);
-- name: Membership :one
SELECT * FROM memberships WHERE space_id=$1 AND principal_id=$2 AND ended_at IS NULL;
-- name: GetMember :one
SELECT * FROM memberships WHERE space_id=$1 AND id=$2 AND ended_at IS NULL;
-- name: MembershipEpisode :one
SELECT * FROM memberships WHERE space_id=$1 AND id=$2;
-- name: ListMembers :many
SELECT m.id,m.role,p.display_name FROM memberships m JOIN principals p ON p.id=m.principal_id
WHERE m.space_id=$1 AND m.ended_at IS NULL AND m.id>$2 ORDER BY m.id LIMIT $3;
-- name: EndMembership :exec
UPDATE memberships SET ended_at=clock_timestamp() WHERE id=$1;
-- name: Role :exec
UPDATE memberships SET role=$2 WHERE id=$1;
-- name: RevokeSpaceArtifacts :exec
UPDATE spaces SET access_epoch=access_epoch+1,version=version+1 WHERE id=$1;
-- name: InsertSpaceAccess :exec
INSERT INTO space_access(space_id,source,profile,state,valid_from,valid_until,billing_member)
VALUES ($1,'development',$2,'active',$3,$4,$5);
-- name: GetSpaceAccess :one
SELECT * FROM space_access WHERE space_id=$1 FOR UPDATE;
-- name: InsertCapability :exec
INSERT INTO space_capabilities(space_id,capability,ceiling,used) VALUES ($1,$2,$3,$4);
-- name: Capability :one
SELECT * FROM space_capabilities WHERE space_id=$1 AND capability=$2 FOR UPDATE;
-- name: ConsumeCapability :execrows
UPDATE space_capabilities SET used=used+$3 WHERE space_id=$1 AND capability=$2 AND used+$3>=0 AND used+$3<=ceiling;
-- name: ReleaseMember :execrows
UPDATE space_capabilities SET used=used-1 WHERE space_id=$1 AND capability='members' AND used>0;
-- name: ListCapabilities :many
SELECT capability,ceiling,used FROM space_capabilities WHERE space_id=$1 ORDER BY capability LIMIT 100;
-- name: ClaimBilling :exec
UPDATE space_access SET billing_member=$2,version=version+1 WHERE space_id=$1;
-- name: InsertInvitation :exec
INSERT INTO invitations(id,space_id,inviter,email,role,verifier,state,expires_at)
VALUES ($1,$2,$3,$4,$5,$6,'pending',$7);
-- name: FindInvitation :one
SELECT * FROM invitations WHERE verifier=$1;
-- name: GetInvitation :one
SELECT * FROM invitations WHERE space_id=$1 AND id=$2 FOR UPDATE;
-- name: ListInvitations :many
SELECT id,email,role,(CASE WHEN state='pending' AND expires_at<=clock_timestamp() THEN 'expired' ELSE state END)::text AS state,
expires_at,generation FROM invitations WHERE space_id=$1 AND id>$2 ORDER BY id LIMIT $3;
-- name: ExpireInvitations :exec
UPDATE invitations SET state='expired' WHERE space_id=$1 AND state='pending' AND expires_at<=clock_timestamp();
-- name: ResendInvitation :exec
UPDATE invitations SET verifier=$2,generation=generation+1,sent_at=clock_timestamp(),expires_at=$3 WHERE id=$1;
-- name: InvitationState :exec
UPDATE invitations SET state=$2,accepted_member=$3 WHERE id=$1;
-- name: ReplaceSpaceAccess :execrows
UPDATE space_access SET source=$3,profile=$4,source_ref=$5,state=$6,valid_from=$7,valid_until=$8,version=version+1
WHERE space_id=$1 AND version=$2;
-- name: DisableCapabilities :exec
UPDATE space_capabilities SET ceiling=0 WHERE space_id=$1;
-- name: UpsertCapability :exec
INSERT INTO space_capabilities(space_id,capability,ceiling) VALUES ($1,$2,$3)
ON CONFLICT(space_id,capability) DO UPDATE SET ceiling=EXCLUDED.ceiling;
-- name: ScrubTokenRecipients :execrows
UPDATE identity_tokens SET target_email='' WHERE id IN
(SELECT id FROM identity_tokens WHERE expires_at<=clock_timestamp() AND target_email<>'' ORDER BY expires_at LIMIT 1000);
-- name: ScrubDeliveredRecipients :execrows
UPDATE access_deliveries SET target_email='',ciphertext=''::bytea WHERE id IN
(SELECT id FROM access_deliveries WHERE expires_at<=clock_timestamp() AND target_email<>'' ORDER BY expires_at LIMIT 1000);
-- name: PurgePendingMFA :execrows
UPDATE principals SET mfa_pending=NULL,mfa_pending_until=NULL
WHERE mfa_pending_until<=clock_timestamp() AND id IN
(SELECT id FROM principals WHERE mfa_pending_until<=clock_timestamp() ORDER BY mfa_pending_until,id LIMIT 1000 FOR UPDATE SKIP LOCKED);
