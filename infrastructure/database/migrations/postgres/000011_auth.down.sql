-- reverse: create index "user_instance_tags_label" to table: "user_instance_tags"
DROP INDEX "user_instance_tags_label";
-- reverse: create "user_instance_tags" table
DROP TABLE "user_instance_tags";
-- reverse: create index "users_username_uniq" to table: "users"
DROP INDEX "users_username_uniq";
-- reverse: create index "users_oidc_subject_uniq" to table: "users"
DROP INDEX "users_oidc_subject_uniq";
-- reverse: create "users" table
DROP TABLE "users";
