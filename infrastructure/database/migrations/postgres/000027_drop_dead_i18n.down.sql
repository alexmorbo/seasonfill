-- reverse: drop "production_companies_i18n" table
CREATE TABLE "production_companies_i18n" (
  "company_id" bigint NOT NULL,
  "language" text NOT NULL,
  "name" text NOT NULL,
  "description" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("company_id", "language"),
  CONSTRAINT "production_companies_i18n_company_id_fkey" FOREIGN KEY ("company_id") REFERENCES "production_companies" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
CREATE INDEX "production_companies_i18n_name" ON "production_companies_i18n" ("language", "name");
-- reverse: drop "networks_i18n" table
CREATE TABLE "networks_i18n" (
  "network_id" bigint NOT NULL,
  "language" text NOT NULL,
  "name" text NOT NULL,
  "description" text NULL,
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY ("network_id", "language"),
  CONSTRAINT "networks_i18n_network_id_fkey" FOREIGN KEY ("network_id") REFERENCES "networks" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
CREATE INDEX "networks_i18n_name" ON "networks_i18n" ("language", "name");
