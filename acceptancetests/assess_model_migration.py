#!/usr/bin/env python
"""Adding extra text for a commit."""
"""Tests for the Model Migration feature"""

from __future__ import print_function

import argparse
from contextlib import contextmanager
from distutils.version import (
    LooseVersion,
    StrictVersion
    )
import logging
import os
from subprocess import CalledProcessError
import sys
from time import sleep
from urllib2 import urlopen
import yaml

from assess_user_grant_revoke import User
from deploy_stack import (
    BootstrapManager,
    get_random_string
    )
from jujupy.client import (
    BaseCondition,
    get_stripped_version_number,
)
from jujupy.version_client import ModelClient2_0
from jujucharm import local_charm_path
from remote import remote_from_address
from utility import (
    JujuAssertionError,
    add_basic_testing_arguments,
    configure_logging,
    qualified_model_name,
    get_unit_ipaddress,
    temp_dir,
    until_timeout,
    )


__metaclass__ = type


log = logging.getLogger("assess_model_migration")


def assess_model_migration(bs1, bs2, args):
    with bs1.booted_context(args.upload_tools):
        bs1.client.enable_feature('migration')
        bs2.client.enable_feature('migration')
        bs2.client.env.juju_home = bs1.client.env.juju_home
        with bs2.existing_booted_context(args.upload_tools):
            source_client = bs2.client
            dest_client = bs1.client
            # Capture the migrated client so we can use it to assert it
            # continues to operate after the originating controller is torn
            # down.
            results = ensure_migration_with_resources_succeeds(
                source_client,
                dest_client)
            migrated_client, application, resource_contents = results

            ensure_model_logs_are_migrated(source_client, dest_client)
            assess_user_permission_model_migrations(source_client, dest_client)
            if args.use_develop:
                assess_development_branch_migrations(
                    source_client, dest_client)

        if client_is_at_least_2_1(bs1.client):
            # Continue test where we ensure that a migrated model continues to
            # work after it's originating controller has been destroyed.
            assert_model_migrated_successfully(
                migrated_client, application, resource_contents)
        log.info(
            'SUCCESS: Model operational after origin controller destroyed')


def assess_user_permission_model_migrations(source_client, dest_client):
    """Run migration tests for user permissions."""
    with temp_dir() as temp:
        ensure_migrating_with_insufficient_user_permissions_fails(
            source_client, dest_client, temp)
        ensure_migrating_with_superuser_user_permissions_succeeds(
            source_client, dest_client, temp)


def assess_development_branch_migrations(source_client, dest_client):
    with temp_dir() as temp:
        ensure_superuser_can_migrate_other_user_models(
                source_client, dest_client, temp)
    ensure_migration_rolls_back_on_failure(source_client, dest_client)
    ensure_api_login_redirects(source_client, dest_client)


def client_is_at_least_2_1(client):
    """Return true of the given ModelClient is version 2.1 or greater."""
    return not isinstance(client, ModelClient2_0)


def after_22beta4(client_version):
    # LooseVersion considers 2.2-somealpha to be newer than 2.2.0.
    # Attempt strict versioning first.
    client_version = get_stripped_version_number(client_version)
    try:
        return StrictVersion(client_version) >= StrictVersion('2.2.0')
    except ValueError:
        pass
    return LooseVersion(client_version) >= LooseVersion('2.2-beta4')


def parse_args(argv):
    """Parse all arguments."""
    parser = argparse.ArgumentParser(
        description="Test model migration feature")
    add_basic_testing_arguments(parser)
    parser.add_argument(
        '--use-develop',
        action='store_true',
        help='Run tests that rely on features in the develop branch.')
    return parser.parse_args(argv)


def get_bootstrap_managers(args):
    """Create 2 bootstrap managers from the provided args.

    Need to make a couple of elements uniqe (e.g. environment name) so we can
    have 2 bootstrapped at the same time.
    """
    bs_1 = BootstrapManager.from_args(args)
    bs_2 = BootstrapManager.from_args(args)
    # Give the second a separate/unique name.
    bs_2.temp_env_name = '{}-b'.format(bs_1.temp_env_name)
    bs_1.log_dir = _new_log_dir(args.logs, 'a')
    bs_2.log_dir = _new_log_dir(args.logs, 'b')
    return bs_1, bs_2


def _new_log_dir(log_dir, post_fix):
    new_log_dir = os.path.join(log_dir, 'env-{}'.format(post_fix))
    os.mkdir(new_log_dir)
    return new_log_dir


class ModelCheckFailed(Exception):
    """Exception used to signify a model status check failed or timed out."""


def _wait_for_model_check(client, model_check, timeout):
    """Wrapper to have a client wait for a model_check callable to succeed.

    :param client: ModelClient object to act on and pass into model_check
    :param model_check: Callable that takes a ModelClient object. When the
      callable reaches a success state it returns True. If model_check never
      returns True within `timeout`, the exception ModelCheckFailed will be
      raised.
    """
    with client.check_timeouts():
        with client.ignore_soft_deadline():
            for _ in until_timeout(timeout):
                if model_check(client):
                    return
                sleep(1)
    raise ModelCheckFailed()


def wait_until_model_disappears(client, model_name, timeout=60):
    """Waits for a while for 'model_name' model to no longer be listed.

    :raises JujuAssertionError: If the named model continues to be listed in
      list-models after specified timeout.
    """
    def model_check(client):
        try:
            models = client.get_controller_client().get_models()
        except CalledProcessError as e:
            # It's possible that we've tried to get status from the model as
            # it's being removed.
            # We can't consider the model gone yet until we don't get this
            # error and the model is no longer in the output.
            if 'cannot get model details' not in e.stderr:
                raise
        else:
            # 2.2-rc1 introduced new model listing output name/short-name.
            all_model_names = [
                m.get('short-name', m['name']) for m in models['models']]
            if model_name not in all_model_names:
                return True

    try:
        _wait_for_model_check(client, model_check, timeout)
    except ModelCheckFailed:
        raise JujuAssertionError(
            'Model "{}" failed to be removed after {} seconds'.format(
                model_name, timeout))


def wait_for_model(client, model_name, timeout=60):
    """Wait for a given timeout for the client to see the model_name.

    :raises JujuAssertionError: If the named model does not appear in the
      specified timeout.
    """
    def model_check(client):
        models = client.get_controller_client().get_models()
        # 2.2-rc1 introduced new model listing output name/short-name.
        all_model_names = [
            m.get('short-name', m['name']) for m in models['models']]
        if model_name in all_model_names:
            return True
    try:
        _wait_for_model_check(client, model_check, timeout)
    except ModelCheckFailed:
        raise JujuAssertionError(
            'Model "{}" failed to appear after {} seconds'.format(
                model_name, timeout))


def wait_for_migrating(client, timeout=60):
    """Block until provided model client has a migration status.

    :raises JujuAssertionError: If the status doesn't show migration within the
        `timeout` period.
    """
    model_name = client.env.environment
    with client.check_timeouts():
        with client.ignore_soft_deadline():
            for _ in until_timeout(timeout):
                model_details = client.show_model(model_name)
                migration_status = model_details[model_name]['status'].get(
                    'migration')
                if migration_status is not None:
                    return
                sleep(1)
            raise JujuAssertionError(
                'Model \'{}\' failed to start migration after'
                '{} seconds'.format(
                    model_name, timeout
                ))


def assert_deployed_charm_is_responding(client, expected_output=None):
    """Ensure that the deployed simple-server charm is still responding."""
    # Set default value if needed.
    if expected_output is None:
        expected_output = 'simple-server.'
    ipaddress = get_unit_ipaddress(client, 'simple-resource-http/0')
    if expected_output != get_server_response(ipaddress):
        raise JujuAssertionError('Server charm is not responding as expected.')


def get_server_response(ipaddress):
    return urlopen('http://{}'.format(ipaddress)).read().rstrip()


def ensure_api_login_redirects(source_client, dest_client):
    """Login attempts must get transparently redirected to the new controller.
    """
    new_model_client = deploy_dummy_source_to_new_model(
        source_client, 'api-redirection')

    # show model controller details
    before_model_details = source_client.show_model()
    assert_model_has_correct_controller_uuid(source_client)

    log.info('Attempting migration process')

    migrated_model_client = migrate_model_to_controller(
        new_model_client, dest_client)

    # check show model controller details
    assert_model_has_correct_controller_uuid(migrated_model_client)

    after_migration_details = migrated_model_client.show_model()
    before_controller_uuid = before_model_details[
        source_client.env.environment]['controller-uuid']
    after_controller_uuid = after_migration_details[
        migrated_model_client.env.environment]['controller-uuid']
    if before_controller_uuid == after_controller_uuid:
        raise JujuAssertionError()

    # Check file for details.
    assert_data_file_lists_correct_controller_for_model(
        migrated_model_client,
        expected_controller=dest_client.env.controller.name)


def assert_data_file_lists_correct_controller_for_model(
        client, expected_controller):
    models_path = os.path.join(client.env.juju_home, 'models.yaml')
    with open(models_path, 'rt') as f:
        models_data = yaml.safe_load(f)

    controller_models = models_data[
        'controllers'][expected_controller]['models']

    if client.env.environment not in controller_models:
        raise JujuAssertionError()


def assert_model_has_correct_controller_uuid(client):
    model_details = client.show_model()
    model_controller_uuid = model_details[
        client.env.environment]['controller-uuid']
    controller_uuid = client.get_controller_uuid()
    if model_controller_uuid != controller_uuid:
        raise JujuAssertionError()


def ensure_migration_with_resources_succeeds(source_client, dest_client):
    """Test simple migration of a model to another controller.

    Ensure that migration a model that has an application, that uses resources,
    deployed upon it is able to continue it's operation after the migration
    process. This includes assertion that the resources are migrated correctly
    too.

    Given 2 bootstrapped environments:
      - Deploy an application with a resource
        - ensure it's operating as expected
      - Migrate that model to the other environment
        - Ensure it's operating as expected
        - Add a new unit to the application to ensure the model is functional

    :return: Tuple containing migrated client object and the resource string
      that the charm deployed to it outputs.

    """
    resource_contents = get_random_string()
    test_model, application = deploy_simple_server_to_new_model(
        source_client, 'example-model-resource', resource_contents)
    migration_target_client = migrate_model_to_controller(
        test_model, dest_client)
    assert_model_migrated_successfully(
        migration_target_client, application, resource_contents)

    log.info('SUCCESS: resources migrated')
    return migration_target_client, application, resource_contents


def assert_model_migrated_successfully(
        client, application, resource_contents=None):
    client.wait_for_workloads()
    assert_deployed_charm_is_responding(client, resource_contents)
    ensure_model_is_functional(client, application)


def ensure_superuser_can_migrate_other_user_models(
        source_client, dest_client, tmp_dir):

    norm_source_client, norm_dest_client = create_user_on_controllers(
        source_client, dest_client, tmp_dir, 'normaluser', 'addmodel')

    attempt_client = deploy_dummy_source_to_new_model(
        norm_source_client, 'supernormal-test')

    log.info('Showing all models available.')
    source_client.controller_juju('models', ('--all',))

    user_qualified_model_name = qualified_model_name(
        attempt_client.env.environment,
        attempt_client.env.user_name)

    if after_22beta4(source_client.version):
        source_client.juju(
            'migrate',
            (user_qualified_model_name, dest_client.env.controller.name),
            include_e=False)
    else:
        source_client.controller_juju(
            'migrate',
            (user_qualified_model_name, dest_client.env.controller.name))

    migration_client = dest_client.clone(
        dest_client.env.clone(user_qualified_model_name))
    wait_for_model(
        migration_client, user_qualified_model_name)
    migration_client.wait_for_started()
    wait_until_model_disappears(source_client, user_qualified_model_name)


def deploy_simple_server_to_new_model(
        client, model_name, resource_contents=None):
    # As per bug LP:1709773 deploy 2 primary apps and have a subordinate
    #  related to both
    new_model = client.add_model(client.env.clone(model_name))
    application = deploy_simple_resource_server(new_model, resource_contents)
    _, deploy_complete = new_model.deploy('cs:ubuntu')
    new_model.wait_for(deploy_complete)
    new_model.deploy('cs:ntp')
    new_model.juju('add-relation', ('ntp', application))
    new_model.juju('add-relation', ('ntp', 'ubuntu'))
    # Need to wait for the subordinate charms too.
    new_model.wait_for(AllApplicationActive())
    new_model.wait_for(AllApplicationWorkloads())
    new_model.juju('status', ())
    assert_deployed_charm_is_responding(new_model, resource_contents)

    return new_model, application


class AllApplicationActive(BaseCondition):
    """Ensure all applications (incl. subordinates) are 'active' state."""

    def iter_blocking_state(self, status):
        applications = status.get_applications()
        all_app_status = [
            state['application-status']['current']
            for name, state in applications.items()]
        apps_active = [state == 'active' for state in all_app_status]
        if not all(apps_active):
            yield 'applications', 'not-all-active'

    def do_raise(self, model_name, status):
        raise Exception('Timed out waiting for all applications to be active.')


class AllApplicationWorkloads(BaseCondition):
    """Ensure all applications (incl. subordinates) are workload 'active'."""

    def iter_blocking_state(self, status):
        app_workloads_active = []
        for name, unit in status.iter_units():
            try:
                state = unit['workload-status']['current'] == 'active'
            except KeyError:
                state = False
            app_workloads_active.append(state)
        if not all(app_workloads_active):
            yield 'application-workloads', 'not-all-active'

    def do_raise(self, model_name, status):
        raise Exception(
            'Timed out waiting for all application workloads to be active.')


def deploy_dummy_source_to_new_model(client, model_name):
    new_model_client = client.add_model(client.env.clone(model_name))
    charm_path = local_charm_path(
        charm='dummy-source', juju_ver=new_model_client.version)
    new_model_client.deploy(charm_path)
    new_model_client.wait_for_started()
    new_model_client.set_config('dummy-source', {'token': 'one'})
    new_model_client.wait_for_workloads()
    return new_model_client


def deploy_simple_resource_server(client, resource_contents=None):
    application_name = 'simple-resource-http'
    log.info('Deploying charm: '.format(application_name))
    charm_path = local_charm_path(
        charm=application_name, juju_ver=client.version)
    # Create a temp file which we'll use as the resource.
    if resource_contents is not None:
        with temp_dir() as temp:
            index_file = os.path.join(temp, 'index.html')
            with open(index_file, 'wt') as f:
                f.write(resource_contents)
            client.deploy(charm_path, resource='index={}'.format(index_file))
    else:
        client.deploy(charm_path)

    client.wait_for_started()
    client.wait_for_workloads()
    client.juju('expose', (application_name))
    return application_name


def migrate_model_to_controller(
        source_client, dest_client, include_user_name=False):
    log.info('Initiating migration process')
    if include_user_name:
        if after_22beta4(source_client.version):
            model_name = '{}:{}/{}'.format(
                source_client.env.controller.name,
                source_client.env.user_name,
                source_client.env.environment)
        else:
            model_name = '{}/{}'.format(
                source_client.env.user_name, source_client.env.environment)
    else:
        if after_22beta4(source_client.version):
            model_name = '{}:{}'.format(
                source_client.env.controller.name,
                source_client.env.environment)
        else:
            model_name = source_client.env.environment

    if after_22beta4(source_client.version):
        source_client.juju(
            'migrate',
            (model_name, dest_client.env.controller.name),
            include_e=False)
    else:
        source_client.controller_juju(
            'migrate', (model_name, dest_client.env.controller.name))
    migration_target_client = dest_client.clone(
        dest_client.env.clone(source_client.env.environment))
    wait_for_model(migration_target_client, source_client.env.environment)
    migration_target_client.wait_for_started()
    wait_until_model_disappears(source_client, source_client.env.environment)
    return migration_target_client


def ensure_model_is_functional(client, application):
    """Ensures that the migrated model is functional

    Add unit to application to ensure the model is contactable and working.
    Ensure that added unit is created on a new machine (check for bug
    LP:1607599)
    """
    # Ensure model returns status before adding units
    client.get_status()
    client.juju('add-unit', (application,))
    client.wait_for_started()
    assert_units_on_different_machines(client, application)
    log.info('SUCCESS: migrated model is functional.')


def assert_units_on_different_machines(client, application):
    status = client.get_status()
    # Not all units will be machines (as we have subordinate apps.)
    unit_machines = [
        u[1]['machine'] for u in status.iter_units()
        if u[1].get('machine', None)]
    raise_if_shared_machines(unit_machines)


def raise_if_shared_machines(unit_machines):
    """Raise an exception if `unit_machines` contain double ups of machine ids.

    A unique list of machine ids will be equal in length to the set of those
    machine ids.

    :raises ValueError: if an empty list is passed in.
    :raises JujuAssertionError: if any double-ups of machine ids are detected.
    """
    if not unit_machines:
        raise ValueError('Cannot share 0 machines. Empty list provided.')
    if len(unit_machines) != len(set(unit_machines)):
        raise JujuAssertionError('Appliction units reside on the same machine')


def ensure_model_logs_are_migrated(source_client, dest_client, timeout=60):
    """Ensure logs are migrated when a model is migrated between controllers.

    :param source_client: ModelClient representing source controller to create
      model on and migrate that model from.
    :param dest_client: ModelClient for destination controller to migrate to.
    :param timeout: int seconds to wait for logs to appear in migrated model.
    """
    new_model_client = deploy_dummy_source_to_new_model(
        source_client, 'log-migration')
    before_migration_logs = new_model_client.get_juju_output(
        'debug-log', '--no-tail', '-l', 'DEBUG')
    log.info('Attempting migration process')
    migrated_model = migrate_model_to_controller(new_model_client, dest_client)

    assert_logs_appear_in_client_model(
        migrated_model, before_migration_logs, timeout)


def assert_logs_appear_in_client_model(client, expected_logs, timeout):
    """Assert that `expected_logs` appear in client logs within timeout.

    :param client: ModelClient object to query logs of.
    :param expected_logs: string containing log contents to check for.
    :param timeout: int seconds to wait for before raising JujuAssertionError.
    """
    for _ in until_timeout(timeout):
        current_logs = client.get_juju_output(
            'debug-log', '--no-tail', '--replay', '-l', 'DEBUG')
        if expected_logs in current_logs:
            log.info('SUCCESS: logs migrated.')
            return
        sleep(1)
    raise JujuAssertionError(
        'Logs failed to be migrated after {}'.format(timeout))


def ensure_migration_rolls_back_on_failure(source_client, dest_client):
    """Must successfully roll back migration when migration fails.

    If the target controller becomes unavailable for the migration to complete
    the migration must roll back and continue to be available on the source
    controller.
    """
    test_model, application = deploy_simple_server_to_new_model(
        source_client, 'rollmeback')
    if after_22beta4(source_client.version):
        test_model.juju(
            'migrate',
            (test_model.env.environment, dest_client.env.controller.name),
            include_e=False)
    else:
        test_model.controller_juju(
            'migrate',
            (test_model.env.environment, dest_client.env.controller.name))
    # Once migration has started interrupt it
    wait_for_migrating(test_model)
    log.info('Disrupting target controller to force rollback')
    with disable_apiserver(dest_client.get_controller_client()):
        # Wait for model to be back and working on the original controller.
        log.info('Waiting for migration rollback to complete.')
        wait_for_model(test_model, test_model.env.environment)
        test_model.wait_for_started()
        assert_deployed_charm_is_responding(test_model)
        ensure_model_is_functional(test_model, application)
    test_model.remove_service(application)
    log.info('SUCCESS: migration rolled back.')


@contextmanager
def disable_apiserver(admin_client, machine_number='0'):
    """Disable the api server on the machine number provided.

    For the duration of the context manager stop the apiserver process on the
    controller machine.
    """
    rem_client = get_remote_for_controller(admin_client)
    try:
        rem_client.run(
            'sudo service jujud-machine-{} stop'.format(machine_number))
        yield
    finally:
        rem_client.run(
            'sudo service jujud-machine-{} start'.format(machine_number))


def get_remote_for_controller(admin_client):
    """Get a remote client to the controller machine of `admin_client`.

    :return: remote.SSHRemote object for the controller machine.
    """
    status = admin_client.get_status()
    controller_ip = status.get_machine_dns_name('0')
    return remote_from_address(controller_ip)


def ensure_migrating_with_insufficient_user_permissions_fails(
        source_client, dest_client, tmp_dir):
    """Ensure migration fails when a user does not have the right permissions.

    A non-superuser on a controller cannot migrate their models between
    controllers.
    """
    user_source_client, user_dest_client = create_user_on_controllers(
        source_client, dest_client, tmp_dir, 'failuser', 'addmodel')
    user_new_model = deploy_dummy_source_to_new_model(
        user_source_client, 'user-fail')
    log.info('Attempting migration process')
    expect_migration_attempt_to_fail(user_new_model, user_dest_client)


def ensure_migrating_with_superuser_user_permissions_succeeds(
        source_client, dest_client, tmp_dir):
    """Ensure migration succeeds when a user has superuser permissions

    A user with superuser permissions is able to migrate between controllers.
    """
    user_source_client, user_dest_client = create_user_on_controllers(
        source_client, dest_client, tmp_dir, 'passuser', 'superuser')
    user_new_model = deploy_dummy_source_to_new_model(
        user_source_client, 'super-permissions')
    log.info('Attempting migration process')
    migrate_model_to_controller(
        user_new_model, user_dest_client, include_user_name=True)
    log.info('SUCCESS: superuser migrated other user model.')


def create_user_on_controllers(source_client, dest_client,
                               tmp_dir, username, permission):
    """Create a user on both supplied controller with the permissions supplied.

    :param source_client: ModelClient object to create user on.
    :param dest_client: ModelClient object to create user on.
    :param tmp_dir: Path to base new users JUJU_DATA directory in.
    :param username: String of username to use.
    :param permission: String for permissions to grant user on both
      controllers. Valid values are `ModelClient.controller_permissions`.
    """
    new_user_home = os.path.join(tmp_dir, username)
    os.makedirs(new_user_home)
    new_user = User(username, 'write', [])
    source_user_client = source_client.register_user(new_user, new_user_home)
    source_client.grant(new_user.name, permission)
    second_controller_name = '{}_controllerb'.format(new_user.name)
    dest_user_client = dest_client.register_user(
        new_user,
        new_user_home,
        controller_name=second_controller_name)
    dest_client.grant(new_user.name, permission)

    return source_user_client, dest_user_client


def expect_migration_attempt_to_fail(source_client, dest_client):
    """Ensure that the migration attempt fails due to permissions.

    As we're capturing the stderr output it after we're done with it so it
    appears in test logs.
    """
    try:
        if after_22beta4(source_client.version):
            args = [
                '{}:{}'.format(
                    source_client.env.controller.name,
                    source_client.env.environment),
                dest_client.env.controller.name
            ]
        else:
            args = [
                '-c', source_client.env.controller.name,
                source_client.env.environment,
                dest_client.env.controller.name
            ]
        log_output = source_client.get_juju_output(
            'migrate', *args, merge_stderr=True, include_e=False)
    except CalledProcessError as e:
        print(e.output, file=sys.stderr)
        if 'permission denied' not in e.output:
            raise
        log.info('SUCCESS: Migrate command failed as expected.')
    else:
        print(log_output, file=sys.stderr)
        raise JujuAssertionError('Migration did not fail as expected.')


def main(argv=None):
    args = parse_args(argv)
    configure_logging(args.verbose)
    bs1, bs2 = get_bootstrap_managers(args)
    assess_model_migration(bs1, bs2, args)
    return 0


if __name__ == '__main__':
    sys.exit(main())
