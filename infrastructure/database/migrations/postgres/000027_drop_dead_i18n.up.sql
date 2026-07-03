-- Reviewed destructive migration (decision O-3): networks_i18n and
-- production_companies_i18n were never written and have no read path;
-- dropping them is intentional. Down-migration recreates both empty.
-- atlas:nolint destructive
-- drop "networks_i18n" table
DROP TABLE "networks_i18n";
-- atlas:nolint destructive
-- drop "production_companies_i18n" table
DROP TABLE "production_companies_i18n";
