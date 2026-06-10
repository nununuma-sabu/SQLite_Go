create table people (
  id integer primary key,
  name text,
  height real,
  nickname text
);

insert 1 Alice 165.2 null;
insert 2 Bob 172.4 Bobby;
insert 3 Carol 158.9 null;

select name, height from people where id = 2;
select name from people where id != 1;
select name from people where id <> 2;
select name from people where height >= 165.2;
select name from people where height < 170;
select id from people where name > 'Alice';
select id from people where nickname is null;
select id from people where nickname is not null;
select name from people where id >= 2 and height < 180;
select name from people where nickname is null and height < 160;
select name from people where id = 1 or height < 160;
select name from people where id = 1 or id = 2 and height < 170;
select name from people where (id = 1 or id = 2) and height < 170;
select name from people where (nickname is null and height < 160) or name = Bob;
select name, height from people order by height asc;
select name from people where id = 1 or height < 170 order by height desc;
