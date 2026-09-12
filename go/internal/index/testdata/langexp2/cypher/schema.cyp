// Knowledge graph schema + sample queries

CREATE (a:Person {name: 'Alice', age: 30})-[:KNOWS]->(b:Person {name: 'Bob', age: 28});
CREATE (c:Company {name: 'Acme Corp', founded: 2010});
MATCH (p:Person)-[:WORKS_AT]->(c:Company) WHERE c.name = 'Acme Corp' RETURN p;
