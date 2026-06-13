import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useParams } from 'react-router-dom';
import { TriangleAlert } from 'lucide-react';
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert';
import { Skeleton } from '@/components/ui/skeleton';
import { useSetPageTitle } from '@/components/shell/page-title-context';
import { usePerson, isPersonStub, type LibrarySort } from '@/api/person';
import { PersonHero } from '@/components/person-page/PersonHero';
import { BiographySection } from '@/components/person-page/BiographySection';
import { LibraryCreditsGrid } from '@/components/person-page/LibraryCreditsGrid';
import { OtherCreditsGrid } from '@/components/person-page/OtherCreditsGrid';

export function Person() {
  const { t, i18n } = useTranslation();
  const { tmdbId: tmdbIdParam } = useParams<{ tmdbId: string }>();
  const parsed = tmdbIdParam ? Number(tmdbIdParam) : NaN;
  const tmdbId = Number.isFinite(parsed) && parsed > 0 ? parsed : undefined;
  const lang = i18n.resolvedLanguage;
  const [sort, setSort] = useState<LibrarySort>('recent');

  useSetPageTitle(t('person.pageTitle'));

  const result = usePerson({
    tmdbId,
    ...(lang ? { lang } : {}),
    sort,
  });

  if (!tmdbId) {
    return (
      <div className="p-4">
        <Alert variant="destructive">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('person.invalidTmdb')}</AlertTitle>
        </Alert>
      </div>
    );
  }

  if (result.isPending) {
    return (
      <div data-testid="person-skeleton" className="flex flex-col gap-6">
        <div className="flex flex-col md:flex-row gap-4 md:gap-6">
          <Skeleton className="w-full max-w-[240px] aspect-[2/3] rounded-lg" />
          <div className="flex flex-col gap-2 flex-1">
            <Skeleton className="h-7 w-[60%] rounded" />
            <Skeleton className="h-4 w-[40%] rounded" />
            <Skeleton className="h-4 w-[80%] rounded" />
            <Skeleton className="h-4 w-[70%] rounded" />
          </div>
        </div>
        <Skeleton className="h-5 w-[20%] rounded" />
        <div className="grid gap-3 grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-5">
          {Array.from({ length: 5 }).map((_, i) => (
            <Skeleton key={i} className="aspect-[2/3] w-full rounded-lg" />
          ))}
        </div>
      </div>
    );
  }

  if (result.isError) {
    return (
      <div className="p-4">
        <Alert variant="destructive" data-testid="person-error">
          <TriangleAlert className="h-4 w-4" />
          <AlertTitle>{t('person.loadFailedTitle')}</AlertTitle>
          <AlertDescription>{t('person.loadFailedBody')}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const data = result.data;
  const person = data?.person;
  const library = data?.library_credits ?? [];
  const other = data?.other_credits ?? [];
  const isStub = isPersonStub(data);
  const hasNoData = !person?.name && !data?.biography && library.length === 0 && other.length === 0;

  return (
    <div className="flex flex-col gap-6">
      <PersonHero person={person} />

      {isStub && (
        <div
          data-testid="person-stub-note"
          className="text-[12px] text-tx-muted italic"
        >
          {t('person.loadingDetails')}
        </div>
      )}

      {hasNoData && !isStub && (
        <p data-testid="person-limited" className="text-[13px] text-tx-muted italic">
          {t('person.limitedData')}
        </p>
      )}

      <BiographySection
        biography={data?.biography}
        bioLanguage={data?.bio_language}
        uiLanguage={lang}
        sync={data?.sync}
      />

      <LibraryCreditsGrid
        credits={library}
        sort={sort}
        onSortChange={setSort}
      />

      <OtherCreditsGrid credits={other} />
    </div>
  );
}
