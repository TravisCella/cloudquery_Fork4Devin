-- SecVuln Check: Detect S3 buckets that are potentially publicly accessible
--
-- NEW SCHEMA (post-migration):
--   Public access block settings now live in the child table
--   aws_s3_bucket_public_access_blocks, joined to aws_s3_buckets via bucket_arn.
--
-- A bucket is flagged as a security vulnerability if ANY of the four public
-- access block settings is not explicitly set to true, or if the bucket has
-- no public access block configuration at all.
--
-- DEPRECATED equivalent (old flat-column schema):
--   SELECT arn, account_id, name, region
--   FROM aws_s3_buckets
--   WHERE block_public_acls       IS NOT TRUE
--      OR block_public_policy     IS NOT TRUE
--      OR ignore_public_acls      IS NOT TRUE
--      OR restrict_public_buckets IS NOT TRUE;

SELECT
    b.arn                        AS bucket_arn,
    b.account_id,
    b.name,
    b.region,
    COALESCE(
        (pab.public_access_block_configuration->>'BlockPublicAcls')::boolean,
        false
    )                            AS block_public_acls,
    COALESCE(
        (pab.public_access_block_configuration->>'BlockPublicPolicy')::boolean,
        false
    )                            AS block_public_policy,
    COALESCE(
        (pab.public_access_block_configuration->>'IgnorePublicAcls')::boolean,
        false
    )                            AS ignore_public_acls,
    COALESCE(
        (pab.public_access_block_configuration->>'RestrictPublicBuckets')::boolean,
        false
    )                            AS restrict_public_buckets,
    CASE
        WHEN pab._cq_id IS NULL THEN 'CRITICAL: No public access block configured'
        WHEN (pab.public_access_block_configuration->>'BlockPublicAcls')::boolean IS NOT TRUE
          OR (pab.public_access_block_configuration->>'BlockPublicPolicy')::boolean IS NOT TRUE
          OR (pab.public_access_block_configuration->>'IgnorePublicAcls')::boolean IS NOT TRUE
          OR (pab.public_access_block_configuration->>'RestrictPublicBuckets')::boolean IS NOT TRUE
        THEN 'WARNING: Incomplete public access block'
        ELSE 'PASS'
    END                          AS secvuln_status
FROM aws_s3_buckets b
LEFT JOIN aws_s3_bucket_public_access_blocks pab
    ON pab.bucket_arn = b.arn
WHERE pab._cq_id IS NULL
   OR (pab.public_access_block_configuration->>'BlockPublicAcls')::boolean IS NOT TRUE
   OR (pab.public_access_block_configuration->>'BlockPublicPolicy')::boolean IS NOT TRUE
   OR (pab.public_access_block_configuration->>'IgnorePublicAcls')::boolean IS NOT TRUE
   OR (pab.public_access_block_configuration->>'RestrictPublicBuckets')::boolean IS NOT TRUE
ORDER BY secvuln_status, b.account_id, b.arn;
