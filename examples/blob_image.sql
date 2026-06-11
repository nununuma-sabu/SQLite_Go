create or replace table images (
  id integer primary key,
  name text,
  data blob
);

insert 1 usagi @usagi.png;
select id, name, data from images;
