-- modify "users" table
ALTER TABLE "users" ADD COLUMN "jellyfin_user_id" text NULL;
-- create index "users_jellyfin_user_id_uniq" to table: "users"
CREATE UNIQUE INDEX "users_jellyfin_user_id_uniq" ON "users" ("jellyfin_user_id") WHERE (jellyfin_user_id IS NOT NULL);
