-- Rollback: Reverse the ETL migration from nested child tables back to flat columns
--
-- Use this script ONLY if you need to revert to the deprecated flat-column
-- schema on aws_s3_buckets. After rolling back, you must also downgrade the
-- AWS source plugin to a version prior to the nested-table refactor.

BEGIN;

-- 1. Re-add the deprecated columns to aws_s3_buckets

ALTER TABLE aws_s3_buckets
    ADD COLUMN IF NOT EXISTS block_public_acls       boolean,
    ADD COLUMN IF NOT EXISTS block_public_policy      boolean,
    ADD COLUMN IF NOT EXISTS ignore_public_acls       boolean,
    ADD COLUMN IF NOT EXISTS restrict_public_buckets  boolean,
    ADD COLUMN IF NOT EXISTS policy                   jsonb,
    ADD COLUMN IF NOT EXISTS versioning_status        text,
    ADD COLUMN IF NOT EXISTS versioning_mfa_delete    text,
    ADD COLUMN IF NOT EXISTS logging_target_bucket    text,
    ADD COLUMN IF NOT EXISTS logging_target_prefix    text,
    ADD COLUMN IF NOT EXISTS replication_role          text,
    ADD COLUMN IF NOT EXISTS replication_rules         jsonb,
    ADD COLUMN IF NOT EXISTS ownership_controls        jsonb;

-- 2. Backfill public access block settings

UPDATE aws_s3_buckets b
SET
    block_public_acls       = (pab.public_access_block_configuration->>'BlockPublicAcls')::boolean,
    block_public_policy     = (pab.public_access_block_configuration->>'BlockPublicPolicy')::boolean,
    ignore_public_acls      = (pab.public_access_block_configuration->>'IgnorePublicAcls')::boolean,
    restrict_public_buckets = (pab.public_access_block_configuration->>'RestrictPublicBuckets')::boolean
FROM aws_s3_bucket_public_access_blocks pab
WHERE pab.bucket_arn = b.arn;

-- 3. Backfill bucket policies

UPDATE aws_s3_buckets b
SET policy = bp.policy_json
FROM aws_s3_bucket_policies bp
WHERE bp.bucket_arn = b.arn;

-- 4. Backfill versioning settings

UPDATE aws_s3_buckets b
SET
    versioning_status     = bv.status,
    versioning_mfa_delete = bv.mfa_delete
FROM aws_s3_bucket_versionings bv
WHERE bv.bucket_arn = b.arn;

-- 5. Backfill logging settings

UPDATE aws_s3_buckets b
SET
    logging_target_bucket = bl.logging_enabled->>'TargetBucket',
    logging_target_prefix = bl.logging_enabled->>'TargetPrefix'
FROM aws_s3_bucket_loggings bl
WHERE bl.bucket_arn = b.arn;

-- 6. Backfill replication settings

UPDATE aws_s3_buckets b
SET
    replication_role  = br.replication_configuration->>'Role',
    replication_rules = br.replication_configuration->'Rules'
FROM aws_s3_bucket_replications br
WHERE br.bucket_arn = b.arn;

-- 7. Backfill ownership controls

UPDATE aws_s3_buckets b
SET ownership_controls = (
    SELECT jsonb_agg(boc.object_ownership)
    FROM aws_s3_bucket_ownership_controls boc
    WHERE boc.bucket_arn = b.arn
)
WHERE EXISTS (
    SELECT 1
    FROM aws_s3_bucket_ownership_controls boc
    WHERE boc.bucket_arn = b.arn
);

COMMIT;
