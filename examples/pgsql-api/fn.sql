CREATE OR REPLACE FUNCTION public.calc_total(p_id integer)
RETURNS integer AS $$
BEGIN
  RETURN (SELECT count(*) FROM orders WHERE user_id = p_id);
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION public.update_status(p_id integer, p_status text)
RETURNS void AS $$
BEGIN
  UPDATE users SET status = p_status WHERE id = p_id;
END;
$$ LANGUAGE plpgsql;