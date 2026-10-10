import { Link } from 'react-router';
import { PageHeader } from '@/components/page-state';

export function NotFoundPage() {
  return (
    <>
      <PageHeader title="Page not found" description="This page does not exist in the dashboard." />
      <Link to="/" className="text-sm underline underline-offset-2">
        Back to Overview
      </Link>
    </>
  );
}
