-- reverse: create index "users_jellyfin_user_id_uniq" to table: "users"
DROP INDEX "users_jellyfin_user_id_uniq";
-- reverse: modify "users" table
ALTER TABLE "users" DROP COLUMN "jellyfin_user_id";
