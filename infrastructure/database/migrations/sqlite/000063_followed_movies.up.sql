-- create "followed_movies" table
CREATE TABLE `followed_movies` (
  `user_id` integer NOT NULL,
  `movie_id` integer NOT NULL,
  `created_at` datetime NOT NULL DEFAULT (CURRENT_TIMESTAMP),
  PRIMARY KEY (`user_id`, `movie_id`),
  CONSTRAINT `followed_movies_user_id_fkey` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT `followed_movies_movie_id_fkey` FOREIGN KEY (`movie_id`) REFERENCES `movies` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE
);
