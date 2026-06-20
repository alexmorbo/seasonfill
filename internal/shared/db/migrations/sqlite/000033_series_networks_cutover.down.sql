ALTER TABLE series ADD COLUMN network text;

UPDATE series
   SET network = (
     SELECT n.name
       FROM series_networks sn
       JOIN networks n ON n.id = sn.network_id
      WHERE sn.series_id = series.id
      ORDER BY sn.position ASC, n.id ASC
      LIMIT 1
   );
