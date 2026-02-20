-- ETL Migration: aws_s3_buckets deprecated flat columns -> new nested child tables
--
-- Context:
--   The aws_s3_buckets table previously stored public access block settings,
--   policies, versioning, logging, replication, and ownership controls as flat
--   columns directly on the parent table. These columns have been deprecated
--   and replaced with dedicated child tables that JOIN via bucket_arn.
--
-- This migration extracts data from the deprecated flat columns into the new
-- normalized child tables, preserving referential integrity through _cq_id /
-- _cq_parent_id relationships and bucket_arn foreign keys.
--
-- Run against the destination database (e.g. PostgreSQL) that holds synced
-- CloudQuery data.

BEGIN;

-- 1. Migrate public access block settings
--    Old: aws_s3_buckets.block_public_acls, block_public_policy,
--         ignore_public_acls, restrict_public_buckets
--    New: aws_s3_bucket_public_access_blocks.public_access_block_configuration (json)

INSERT INTO aws_s3_bucket_public_access_blocks (
    _cq_id,
    _cq_parent_id,
    account_id,
    bucket_arn,
    public_access_block_configuration
)
SELECT
    gen_random_uuid()                    AS _cq_id,
    b._cq_id                            AS _cq_parent_id,
    b.account_id,
    b.arn                                AS bucket_arn,
    jsonb_build_object(
        'BlockPublicAcls',       COALESCE(b.block_public_acls, false),
        'BlockPublicPolicy',     COALESCE(b.block_public_policy, false),
        'IgnorePublicAcls',      COALESCE(b.ignore_public_acls, false),
        'RestrictPublicBuckets', COALESCE(b.restrict_public_buckets, false)
    )                                    AS public_access_block_configuration
FROM aws_s3_buckets b
WHERE b.block_public_acls IS NOT NULL
   OR b.block_public_policy IS NOT NULL
   OR b.ignore_public_acls IS NOT NULL
   OR b.restrict_public_buckets IS NOT NULL
ON CONFLICT DO NOTHING;

-- 2. Migrate bucket policies
--    Old: aws_s3_buckets.policy (json)
--    New: aws_s3_bucket_policies.policy_json (json), policy (utf8)

INSERT INTO aws_s3_bucket_policies (
    _cq_id,
    _cq_parent_id,
    account_id,
    bucket_arn,
    policy_json,
    policy
)
SELECT
    gen_random_uuid()          AS _cq_id,
    b._cq_id                  AS _cq_parent_id,
    b.account_id,
    b.arn                      AS bucket_arn,
    b.policy                   AS policy_json,
    b.policy::text             AS policy
FROM aws_s3_buckets b
WHERE b.policy IS NOT NULL
ON CONFLICT DO NOTHING;

-- 3. Migrate bucket versioning settings
--    Old: aws_s3_buckets.versioning_status, versioning_mfa_delete
--    New: aws_s3_bucket_versionings.status (utf8), mfa_delete (utf8)

INSERT INTO aws_s3_bucket_versionings (
    _cq_id,
    _cq_parent_id,
    account_id,
    bucket_arn,
    status,
    mfa_delete
)
SELECT
    gen_random_uuid()          AS _cq_id,
    b._cq_id                  AS _cq_parent_id,
    b.account_id,
    b.arn                      AS bucket_arn,
    b.versioning_status        AS status,
    b.versioning_mfa_delete    AS mfa_delete
FROM aws_s3_buckets b
WHERE b.versioning_status IS NOT NULL
   OR b.versioning_mfa_delete IS NOT NULL
ON CONFLICT DO NOTHING;

-- 4. Migrate bucket logging settings
--    Old: aws_s3_buckets.logging_target_bucket, logging_target_prefix
--    New: aws_s3_bucket_loggings.logging_enabled (json)

INSERT INTO aws_s3_bucket_loggings (
    _cq_id,
    _cq_parent_id,
    account_id,
    bucket_arn,
    logging_enabled
)
SELECT
    gen_random_uuid()          AS _cq_id,
    b._cq_id                  AS _cq_parent_id,
    b.account_id,
    b.arn                      AS bucket_arn,
    jsonb_build_object(
        'TargetBucket', b.logging_target_bucket,
        'TargetPrefix', b.logging_target_prefix
    )                          AS logging_enabled
FROM aws_s3_buckets b
WHERE b.logging_target_bucket IS NOT NULL
ON CONFLICT DO NOTHING;

-- 5. Migrate bucket replication settings
--    Old: aws_s3_buckets.replication_role, replication_rules
--    New: aws_s3_bucket_replications.replication_configuration (json)

INSERT INTO aws_s3_bucket_replications (
    _cq_id,
    _cq_parent_id,
    account_id,
    bucket_arn,
    replication_configuration
)
SELECT
    gen_random_uuid()          AS _cq_id,
    b._cq_id                  AS _cq_parent_id,
    b.account_id,
    b.arn                      AS bucket_arn,
    jsonb_build_object(
        'Role',  b.replication_role,
        'Rules', b.replication_rules
    )                          AS replication_configuration
FROM aws_s3_buckets b
WHERE b.replication_role IS NOT NULL
   OR b.replication_rules IS NOT NULL
ON CONFLICT DO NOTHING;

-- 6. Migrate bucket ownership controls
--    Old: aws_s3_buckets.ownership_controls (list<utf8>)
--    New: aws_s3_bucket_ownership_controls.object_ownership (utf8)
--    Note: one row per ownership control entry

INSERT INTO aws_s3_bucket_ownership_controls (
    _cq_id,
    _cq_parent_id,
    account_id,
    bucket_arn,
    object_ownership
)
SELECT
    gen_random_uuid()                        AS _cq_id,
    b._cq_id                                AS _cq_parent_id,
    b.account_id,
    b.arn                                    AS bucket_arn,
    oc.value                                 AS object_ownership
FROM aws_s3_buckets b,
     jsonb_array_elements_text(b.ownership_controls::jsonb) AS oc(value)
WHERE b.ownership_controls IS NOT NULL
ON CONFLICT DO NOTHING;

-- 7. Drop deprecated columns from the parent table
--    These columns are no longer populated by the new AWS source plugin.

ALTER TABLE aws_s3_buckets
    DROP COLUMN IF EXISTS block_public_acls,
    DROP COLUMN IF EXISTS block_public_policy,
    DROP COLUMN IF EXISTS ignore_public_acls,
    DROP COLUMN IF EXISTS restrict_public_buckets,
    DROP COLUMN IF EXISTS policy,
    DROP COLUMN IF EXISTS versioning_status,
    DROP COLUMN IF EXISTS versioning_mfa_delete,
    DROP COLUMN IF EXISTS logging_target_bucket,
    DROP COLUMN IF EXISTS logging_target_prefix,
    DROP COLUMN IF EXISTS replication_role,
    DROP COLUMN IF EXISTS replication_rules,
    DROP COLUMN IF EXISTS ownership_controls;

COMMIT;
