DELETE FROM audit_event WHERE resource_type = 'onboarding_run';
DELETE FROM gitlab_link WHERE resource_type = 'onboarding_run';
DELETE FROM approval WHERE resource_type = 'onboarding_run';
DROP TABLE IF EXISTS onboarding_run;
