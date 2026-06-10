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
