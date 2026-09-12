// Index + constraint DDL and parameterised queries.

CREATE INDEX person_name IF NOT EXISTS FOR (n:Person) ON (n.name);

CREATE CONSTRAINT company_id FOR (c:Company) REQUIRE c.id IS UNIQUE;

MATCH (n:Person)-[:KNOWS*1..2]->(m:Person)
WHERE n.age > 25
RETURN n.name, m.name;

MATCH (:Company)<-[:WORKS_AT]-(e:Employee)
RETURN e.name;
