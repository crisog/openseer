import { MonitorDetailsContent } from "@/components/monitors/MonitorDetailsContent";

interface MonitorDetailPageProps {
  params: Promise<{
    id: string;
  }>;
}

export default async function MonitorDetailPage({ params }: MonitorDetailPageProps): Promise<React.JSX.Element> {
  const { id } = await params;
  return <MonitorDetailsContent monitorId={id} />;
}