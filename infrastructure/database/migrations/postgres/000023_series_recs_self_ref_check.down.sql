-- reverse: modify "series_recommendations" table
ALTER TABLE "series_recommendations" DROP CONSTRAINT "series_recommendations_no_self_ref";
