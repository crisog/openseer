import { Monitor } from "lucide-react";

export function BrandHeader() {
  return (
    <div className="flex items-center justify-center space-x-3 mb-8">
      <div className="w-10 h-10 bg-primary rounded-lg flex items-center justify-center">
        <Monitor className="w-6 h-6 text-primary-foreground" />
      </div>
      <span className="text-2xl font-bold">OpenSeer</span>
    </div>
  );
}
