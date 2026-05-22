export function Placeholder({ title }: { title: string }) {
  return (
    <div className="max-w-[1440px] mx-auto p-7">
      <h1 className="text-[22px] font-semibold tracking-tight">{title}</h1>
      <p className="text-muted mt-3">Coming in 009d.</p>
    </div>
  );
}
