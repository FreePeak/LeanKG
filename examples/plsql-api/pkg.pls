CREATE OR REPLACE PACKAGE employee_pkg AS
  FUNCTION get_name(p_id NUMBER) RETURN VARCHAR2;
  PROCEDURE update_salary(p_id NUMBER, p_amount NUMBER);
END employee_pkg;
/

CREATE OR REPLACE PACKAGE BODY employee_pkg AS
  FUNCTION get_name(p_id NUMBER) RETURN VARCHAR2 AS
  BEGIN
    RETURN 'Employee ' || p_id;
  END;

  PROCEDURE update_salary(p_id NUMBER, p_amount NUMBER) AS
  BEGIN
    NULL;
  END;
END employee_pkg;
/